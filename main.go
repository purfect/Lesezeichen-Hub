package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type application struct {
	db             *sql.DB
	webFS          fs.FS
	metalPricesMu  sync.RWMutex
	metalPrices    metalPricesPayload
	metalPricesAt  time.Time
	metalPricesErr string
	silverPricesMu sync.RWMutex
	silverPrices   silverPricesPayload
	silverPricesAt time.Time
}

type metalPricesPayload struct {
	GoldEURPerGram   float64   `json:"gold_eur_per_g"`
	SilverEURPerGram float64   `json:"silver_eur_per_g"`
	FetchedAt        time.Time `json:"fetched_at"`
	Cached           bool      `json:"cached"`
	Stale            bool      `json:"stale"`
	LastError        string    `json:"last_error,omitempty"`
}

type silverProduct struct {
	Name      string  `json:"name"`
	Category  string  `json:"category"`
	Price     float64 `json:"price"`
	PriceText string  `json:"price_text"`
	URL       string  `json:"url"`
}

type silverPricesPayload struct {
	BestProduct *silverProduct  `json:"best_product"`
	AllProducts []silverProduct `json:"all_products"`
	FetchedAt   time.Time       `json:"fetched_at"`
	Source      string          `json:"source"`
}

type group struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	SortOrder   int        `json:"sort_order"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	Bookmarks   []bookmark `json:"bookmarks,omitempty"`
}

type bookmark struct {
	ID        int64      `json:"id"`
	GroupID   int64      `json:"group_id"`
	Title     string     `json:"title"`
	URL       string     `json:"url"`
	Notes     string     `json:"notes"`
	Tags      []string   `json:"tags"`
	Favorite  bool       `json:"favorite"`
	Pinned    bool       `json:"pinned"`
	Archived  bool       `json:"archived"`
	SortOrder int        `json:"sort_order"`
	RemindAt  *time.Time `json:"remind_at,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type note struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Content     string     `json:"content"`
	Type        string     `json:"type"`
	BookmarkIDs []int64    `json:"bookmark_ids"`
	Tags        []string   `json:"tags"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type apiError struct {
	Error string `json:"error"`
}

type importPayload struct {
	Groups []importGroup `json:"groups"`
}

type importGroup struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	SortOrder   int              `json:"sort_order"`
	Bookmarks   []importBookmark `json:"bookmarks"`
}

type importBookmark struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Notes     string   `json:"notes"`
	Tags      []string `json:"tags"`
	Favorite  bool     `json:"favorite"`
	Pinned    bool     `json:"pinned"`
	Archived  bool     `json:"archived"`
	SortOrder int      `json:"sort_order"`
	RemindAt  string   `json:"remind_at"`
}

type speedDialImport struct {
	Dials []speedDialDial `json:"dials"`
}

type speedDialDial struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type backupPayload struct {
	Version    int           `json:"version"`
	ExportedAt time.Time     `json:"exported_at"`
	Groups     []backupGroup `json:"groups"`
	Notes      []backupNote  `json:"notes"`
}

type backupGroup struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	SortOrder   int              `json:"sort_order"`
	Bookmarks   []backupBookmark `json:"bookmarks"`
}

type backupBookmark struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	URL       string     `json:"url"`
	Notes     string     `json:"notes"`
	Tags      []string   `json:"tags"`
	Favorite  bool       `json:"favorite"`
	Pinned    bool       `json:"pinned"`
	Archived  bool       `json:"archived"`
	SortOrder int        `json:"sort_order"`
	RemindAt  *time.Time `json:"remind_at,omitempty"`
}

type backupNote struct {
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Type        string   `json:"type"`
	BookmarkIDs []int64  `json:"bookmark_ids"`
	Tags        []string `json:"tags"`
}

//go:embed web/*
var embeddedWebFiles embed.FS

func main() {
	dbPath := envOrDefault("BOOKMARK_DB_PATH", "./data.db")
	addr := envOrDefault("ADDR", ":2222")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := initializeSchema(db); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	app := &application{db: db}
	mux := http.NewServeMux()

	webFS, err := fs.Sub(embeddedWebFiles, "web")
	if err != nil {
		log.Fatalf("init embedded web assets: %v", err)
	}
	app.webFS = webFS

	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		log.Fatalf("read embedded index.html: %v", err)
	}

	mux.HandleFunc("/api/state", app.handleState)
	mux.HandleFunc("/api/export", app.handleExport)
	mux.HandleFunc("/api/import", app.handleImport)
	mux.HandleFunc("/api/backup", app.handleBackup)
	mux.HandleFunc("/api/restore", app.handleRestore)
	mux.HandleFunc("/api/groups", app.handleGroups)
	mux.HandleFunc("/api/groups/", app.handleGroupRoutes)
	mux.HandleFunc("/api/bookmarks", app.handleBookmarks)
	mux.HandleFunc("/api/bookmarks/", app.handleBookmarkRoutes)
	mux.HandleFunc("/api/notes", app.handleNotes)
	mux.HandleFunc("/api/notes/", app.handleNoteRoutes)
	mux.HandleFunc("/api/modules", app.handleModules)
	mux.HandleFunc("/api/module-folder", app.handleModuleFolder)
	mux.HandleFunc("/modules/", app.handleModuleFiles)
	mux.HandleFunc("/api/metal-prices", app.handleMetalPrices)
	mux.HandleFunc("/api/silver-prices", app.handleSilverPrices)
	mux.HandleFunc("/silver-preise", app.handleSilverPricesPage)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(webFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(indexHTML); err != nil {
			log.Printf("write index.html: %v", err)
		}
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           recoverMiddleware(loggingMiddleware(mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Lesezeichen-Server laeuft auf http://localhost%s", addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}

func (app *application) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var payload struct {
		GroupID int64    `json:"group_id"`
		Name    string   `json:"name"`
		Path    string   `json:"path"`
		Notes   string   `json:"notes"`
		Tags    []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	name := strings.TrimSpace(payload.Name)
	rootPath := strings.TrimSpace(payload.Path)
	if payload.GroupID <= 0 || name == "" || rootPath == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("gruppe, name und pfad sind erforderlich"))
		return
	}
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("pfad ist ungueltig"))
		return
	}
	info, err := os.Stat(rootPath)
	if err != nil || !info.IsDir() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("modulpfad ist kein vorhandener ordner"))
		return
	}
	if _, err := os.Stat(filepath.Join(rootPath, "index.html")); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("modul braucht eine index.html"))
		return
	}

	tx, err := app.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()
	var groupExists int
	if err := tx.QueryRowContext(r.Context(), `SELECT 1 FROM groups WHERE id = ?`, payload.GroupID).Scan(&groupExists); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("gruppe nicht gefunden"))
		return
	}
	var moduleID int64
	err = tx.QueryRowContext(r.Context(), `SELECT id FROM modules WHERE name = ?`, name).Scan(&moduleID)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.ExecContext(r.Context(), `INSERT INTO modules(name, root_path) VALUES(?, ?)`, name, rootPath)
		if insertErr != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("modul konnte nicht angelegt werden: %v", insertErr))
			return
		}
		moduleID, _ = result.LastInsertId()
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	} else {
		moduleURL := fmt.Sprintf("/modules/%d/index.html", moduleID)
		var bookmarkCount int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM bookmarks WHERE url = ? AND archived = 0`, moduleURL).Scan(&bookmarkCount); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if bookmarkCount > 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ein aktives Modul mit diesem Namen existiert bereits"))
			return
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE modules SET root_path = ? WHERE id = ?`, rootPath, moduleID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	moduleURL := fmt.Sprintf("/modules/%d/index.html", moduleID)
	var existingBookmarkID int64
	var existingArchived int
	err = tx.QueryRowContext(r.Context(), `SELECT id, archived FROM bookmarks WHERE url = ? LIMIT 1`, moduleURL).Scan(&existingBookmarkID, &existingArchived)
	if err == nil {
		if existingArchived == 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ein aktives Modul mit diesem Namen existiert bereits"))
			return
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks SET group_id = ?, title = ?, notes = ?, tags = ?, favorite = 1, archived = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, payload.GroupID, name, strings.TrimSpace(payload.Notes), serializeTags(payload.Tags), existingBookmarkID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if err := tx.Commit(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": moduleID, "url": moduleURL, "reactivated": true})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, sort_order) VALUES(?, ?, ?, ?, ?, ?, ?)`, payload.GroupID, name, moduleURL, strings.TrimSpace(payload.Notes), serializeTags(payload.Tags), 1, 0); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("modulstart konnte nicht angelegt werden: %v", err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": moduleID, "url": moduleURL})
}

func (app *application) handleModuleFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if runtime.GOOS != "windows" {
		writeErr(w, http.StatusNotImplemented, fmt.Errorf("der Windows-Ordnerdialog ist nur unter Windows verfuegbar"))
		return
	}

	const pickerScript = `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = 'Ordner der lokalen Webanwendung waehlen'; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($dialog.SelectedPath) }`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", pickerScript).Output()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("Windows-Ordnerdialog konnte nicht geoeffnet werden"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": strings.TrimSpace(string(output))})
}

func (app *application) handleModuleFiles(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/modules/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	moduleID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || moduleID <= 0 {
		http.NotFound(w, r)
		return
	}
	var rootPath string
	if err := app.db.QueryRowContext(r.Context(), `SELECT root_path FROM modules WHERE id = ?`, moduleID).Scan(&rootPath); err != nil {
		http.NotFound(w, r)
		return
	}
	relativePath := path.Clean(path.Join(parts[1:]...))
	if relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, "../") || path.IsAbs(relativePath) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, filepath.Join(rootPath, filepath.FromSlash(relativePath)))
}

func initializeSchema(db *sql.DB) error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS bookmarks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			notes TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_id ON bookmarks(group_id);`,
		`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'note',
			bookmark_ids TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS modules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			root_path TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	// Lightweight schema migrations for existing databases.
	migrations := []string{
		`ALTER TABLE bookmarks ADD COLUMN tags TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE bookmarks ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN remind_at DATETIME NULL`,
	}

	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_sort ON bookmarks(group_id, pinned DESC, sort_order ASC, id ASC);`); err != nil {
		return err
	}

	return nil
}

func (app *application) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	groups, err := app.fetchState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (app *application) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" && format != "html" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges format; erlaubt: json, csv, html"))
		return
	}

	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige group_id"))
		return
	}

	groups, err := app.fetchState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if groupID > 0 {
		groups = filterGroupsByID(groups, groupID)
		if len(groups) == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("gruppe nicht gefunden"))
			return
		}
	}

	stamp := time.Now().Format("20060102-150405")
	namePart := "lesezeichen"
	if groupID > 0 {
		namePart = fmt.Sprintf("gruppe-%d", groupID)
	}

	switch format {
	case "json":
		filename := fmt.Sprintf("%s-export-%s.json", namePart, stamp)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := json.NewEncoder(w).Encode(map[string]any{"groups": groups}); err != nil {
			log.Printf("encode export json: %v", err)
		}
	case "csv":
		filename := fmt.Sprintf("%s-export-%s.csv", namePart, stamp)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := writeCSVExport(w, groups); err != nil {
			log.Printf("encode export csv: %v", err)
		}
	case "html":
		filename := fmt.Sprintf("%s-startseite-%s.html", namePart, stamp)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := writeHTMLExport(w, groups); err != nil {
			log.Printf("encode export html: %v", err)
		}
	}
}

func (app *application) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	limitedBody := io.LimitReader(r.Body, 10*1024*1024)
	defer r.Body.Close()
	bodyBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("request body konnte nicht gelesen werden"))
		return
	}

	var payload importPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}

	var speedDial speedDialImport
	isSpeedDialImport := false
	if err := json.Unmarshal(bodyBytes, &speedDial); err == nil && len(speedDial.Dials) > 0 {
		isSpeedDialImport = true
		bookmarks := make([]importBookmark, 0, len(speedDial.Dials))
		for i, dial := range speedDial.Dials {
			bookmarks = append(bookmarks, importBookmark{
				Title:     dial.Title,
				URL:       dial.URL,
				Archived:  false,
				SortOrder: i,
			})
		}

		payload.Groups = []importGroup{
			{
				Name:      "ungruppiert",
				SortOrder: 0,
				Bookmarks: bookmarks,
			},
		}
	}

	if payload.Groups == nil {
		payload.Groups = []importGroup{}
	}

	tx, err := app.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	seenNames := make(map[string]struct{})
	createdGroups := 0
	createdBookmarks := 0
	updatedBookmarks := 0

	for _, g := range payload.Groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("gruppenname ist erforderlich"))
			return
		}

		key := strings.ToLower(name)
		if _, exists := seenNames[key]; exists {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("doppelte gruppe im import: %s", name))
			return
		}
		seenNames[key] = struct{}{}

		var groupID int64
		description := strings.TrimSpace(g.Description)
		err := tx.QueryRowContext(
			r.Context(),
			`SELECT id FROM groups WHERE lower(name) = lower(?) LIMIT 1`,
			name,
		).Scan(&groupID)
		if errors.Is(err, sql.ErrNoRows) {
			result, err := tx.ExecContext(
				r.Context(),
				`INSERT INTO groups(name, description, sort_order) VALUES(?, ?, ?)`,
				name,
				description,
				g.SortOrder,
			)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			groupID, _ = result.LastInsertId()
			createdGroups++
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else {
			if !isSpeedDialImport {
				if _, err := tx.ExecContext(
					r.Context(),
					`UPDATE groups SET description = ?, sort_order = ? WHERE id = ?`,
					description,
					g.SortOrder,
					groupID,
				); err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
			}
		}

		for _, b := range g.Bookmarks {
			title := strings.TrimSpace(b.Title)
			if title == "" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("bookmark-titel ist erforderlich"))
				return
			}

			urlValue := strings.TrimSpace(b.URL)
			if err := validateURL(urlValue); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}

			notes := strings.TrimSpace(b.Notes)
			remindAt, err := parseOptionalDateTime(b.RemindAt)
			if err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges remind_at bei %q", title))
				return
			}
			tagsRaw := serializeTags(b.Tags)
			favoriteInt := boolToInt(b.Favorite)
			pinnedInt := boolToInt(b.Pinned)
			archivedInt := boolToInt(b.Archived)
			var bookmarkID int64
			err = tx.QueryRowContext(
				r.Context(),
				`SELECT id FROM bookmarks WHERE group_id = ? AND url = ? LIMIT 1`,
				groupID,
				urlValue,
			).Scan(&bookmarkID)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.ExecContext(
					r.Context(),
					`INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					groupID,
					title,
					urlValue,
					notes,
					tagsRaw,
					favoriteInt,
					pinnedInt,
					archivedInt,
					b.SortOrder,
					remindAt,
				); err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
				createdBookmarks++
			} else if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			} else {
				if _, err := tx.ExecContext(
					r.Context(),
					`UPDATE bookmarks SET title = ?, notes = ?, tags = ?, favorite = ?, pinned = ?, archived = ?, sort_order = ?, remind_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
					title,
					notes,
					tagsRaw,
					favoriteInt,
					pinnedInt,
					archivedInt,
					b.SortOrder,
					remindAt,
					bookmarkID,
				); err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
				updatedBookmarks++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rollback = false

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"imported_groups":   len(payload.Groups),
		"created_groups":    createdGroups,
		"created_bookmarks": createdBookmarks,
		"updated_bookmarks": updatedBookmarks,
	})
}

func (app *application) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	groups, err := app.fetchState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	bkGroups := make([]backupGroup, 0, len(groups))
	for _, g := range groups {
		bg := backupGroup{
			ID:          g.ID,
			Name:        g.Name,
			Description: g.Description,
			SortOrder:   g.SortOrder,
			Bookmarks:   make([]backupBookmark, 0, len(g.Bookmarks)),
		}
		for _, b := range g.Bookmarks {
			bg.Bookmarks = append(bg.Bookmarks, backupBookmark{
				ID:        b.ID,
				Title:     b.Title,
				URL:       b.URL,
				Notes:     b.Notes,
				Tags:      b.Tags,
				Favorite:  b.Favorite,
				Pinned:    b.Pinned,
				Archived:  b.Archived,
				SortOrder: b.SortOrder,
				RemindAt:  b.RemindAt,
			})
		}
		bkGroups = append(bkGroups, bg)
	}

	rows, err := app.db.QueryContext(r.Context(),
		`SELECT title, content, type, bookmark_ids, tags FROM notes ORDER BY id ASC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	bkNotes := make([]backupNote, 0)
	for rows.Next() {
		var n backupNote
		var bmRaw, tagsRaw string
		if err := rows.Scan(&n.Title, &n.Content, &n.Type, &bmRaw, &tagsRaw); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if n.Type == "vault" && !strings.HasPrefix(n.Content, "vault:v1:") {
			n.Content = ""
		}
		n.BookmarkIDs = parseBookmarkIDs(bmRaw)
		n.Tags = parseTags(tagsRaw)
		bkNotes = append(bkNotes, n)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	payload := backupPayload{
		Version:    1,
		ExportedAt: time.Now(),
		Groups:     bkGroups,
		Notes:      bkNotes,
	}

	stamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("lesezeichen-vollsicherung-%s.json", stamp)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode backup: %v", err)
	}
}

func (app *application) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	limitedBody := io.LimitReader(r.Body, 20*1024*1024)
	defer r.Body.Close()
	bodyBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("request body konnte nicht gelesen werden"))
		return
	}

	var payload backupPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	if payload.Version != 1 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unbekannte backup-version: %d", payload.Version))
		return
	}

	tx, err := app.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	// old bookmark ID → new bookmark ID (for remapping note links)
	bookmarkIDMap := make(map[int64]int64)
	createdGroups, createdBookmarks, updatedBookmarks, createdNotes, updatedNotes := 0, 0, 0, 0, 0

	for _, g := range payload.Groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}

		var groupID int64
		err := tx.QueryRowContext(r.Context(),
			`SELECT id FROM groups WHERE lower(name) = lower(?) LIMIT 1`, name).Scan(&groupID)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(r.Context(),
				`INSERT INTO groups(name, description, sort_order) VALUES(?, ?, ?)`,
				name, strings.TrimSpace(g.Description), g.SortOrder)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			groupID, _ = res.LastInsertId()
			createdGroups++
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else {
			_, _ = tx.ExecContext(r.Context(),
				`UPDATE groups SET description = ?, sort_order = ? WHERE id = ?`,
				strings.TrimSpace(g.Description), g.SortOrder, groupID)
		}

		for _, b := range g.Bookmarks {
			title := strings.TrimSpace(b.Title)
			urlValue := strings.TrimSpace(b.URL)
			if title == "" || urlValue == "" {
				continue
			}
			tagsRaw := serializeTags(b.Tags)

			var newID int64
			err := tx.QueryRowContext(r.Context(),
				`SELECT id FROM bookmarks WHERE group_id = ? AND url = ? LIMIT 1`,
				groupID, urlValue).Scan(&newID)
			if errors.Is(err, sql.ErrNoRows) {
				res, err := tx.ExecContext(r.Context(),
					`INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					groupID, title, urlValue, b.Notes, tagsRaw,
					boolToInt(b.Favorite), boolToInt(b.Pinned), boolToInt(b.Archived),
					b.SortOrder, b.RemindAt)
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err)
					return
				}
				newID, _ = res.LastInsertId()
				createdBookmarks++
			} else if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			} else {
				_, _ = tx.ExecContext(r.Context(),
					`UPDATE bookmarks SET title = ?, notes = ?, tags = ?, favorite = ?, pinned = ?, archived = ?, sort_order = ?, remind_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
					title, b.Notes, tagsRaw,
					boolToInt(b.Favorite), boolToInt(b.Pinned), boolToInt(b.Archived),
					b.SortOrder, b.RemindAt, newID)
				updatedBookmarks++
			}

			if b.ID > 0 {
				bookmarkIDMap[b.ID] = newID
			}
		}
	}

	for _, n := range payload.Notes {
		title := strings.TrimSpace(n.Title)
		if title == "" {
			continue
		}
		noteType := n.Type
		if noteType != "note" && noteType != "code" && noteType != "annotation" && noteType != "vault" {
			noteType = "note"
		}

		// Remap old bookmark IDs → new IDs; drop any that no longer exist
		remapped := make([]int64, 0, len(n.BookmarkIDs))
		for _, oldID := range n.BookmarkIDs {
			if newID, ok := bookmarkIDMap[oldID]; ok {
				remapped = append(remapped, newID)
			}
		}

		bmRaw := serializeBookmarkIDs(remapped)
		tagsRaw := serializeTags(n.Tags)

		var existingID int64
		err := tx.QueryRowContext(r.Context(),
			`SELECT id FROM notes WHERE title = ? LIMIT 1`, title).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(r.Context(),
				`INSERT INTO notes(title, content, type, bookmark_ids, tags) VALUES(?, ?, ?, ?, ?)`,
				title, strings.TrimSpace(n.Content), noteType, bmRaw, tagsRaw)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			createdNotes++
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else {
			_, _ = tx.ExecContext(r.Context(),
				`UPDATE notes SET content = ?, type = ?, bookmark_ids = ?, tags = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				strings.TrimSpace(n.Content), noteType, bmRaw, tagsRaw, existingID)
			updatedNotes++
		}
	}

	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rollback = false

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"created_groups":    createdGroups,
		"created_bookmarks": createdBookmarks,
		"updated_bookmarks": updatedBookmarks,
		"created_notes":     createdNotes,
		"updated_notes":     updatedNotes,
	})
}

func (app *application) handleGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := app.db.QueryContext(r.Context(), `SELECT id, name, description, sort_order, created_at FROM groups ORDER BY sort_order ASC, name ASC`)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		items := make([]group, 0)
		for rows.Next() {
			var g group
			if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.SortOrder, &g.CreatedAt); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			items = append(items, g)
		}
		if err := rows.Err(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"groups": items})
	case http.MethodPost:
		var payload struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			SortOrder   int    `json:"sort_order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}

		payload.Name = strings.TrimSpace(payload.Name)
		if payload.Name == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("name ist erforderlich"))
			return
		}

		res, err := app.db.ExecContext(
			r.Context(),
			`INSERT INTO groups(name, description, sort_order) VALUES(?, ?, ?)`,
			payload.Name,
			strings.TrimSpace(payload.Description),
			payload.SortOrder,
		)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	default:
		methodNotAllowed(w)
	}
}

func (app *application) handleGroupRoutes(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/groups/"), "/")
	segments := strings.Split(trimmed, "/")
	if len(segments) == 0 || segments[0] == "" {
		http.NotFound(w, r)
		return
	}

	if len(segments) == 1 && segments[0] == "reorder" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		app.reorderGroups(w, r)
		return
	}

	groupID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige gruppen-id"))
		return
	}

	if len(segments) == 3 && segments[1] == "bookmarks" && segments[2] == "reorder" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		app.reorderBookmarksInGroup(w, r, groupID)
		return
	}

	if len(segments) == 2 && segments[1] == "bookmarks" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		app.listBookmarksByGroup(w, r, groupID)
		return
	}

	if len(segments) != 1 {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			SortOrder   int    `json:"sort_order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}

		name := strings.TrimSpace(payload.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("name ist erforderlich"))
			return
		}

		res, err := app.db.ExecContext(
			r.Context(),
			`UPDATE groups SET name = ?, description = ?, sort_order = ? WHERE id = ?`,
			name,
			strings.TrimSpace(payload.Description),
			payload.SortOrder,
			groupID,
		)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("gruppe nicht gefunden"))
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		res, err := app.db.ExecContext(r.Context(), `DELETE FROM groups WHERE id = ?`, groupID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("gruppe nicht gefunden"))
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (app *application) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var payload struct {
		GroupID   int64    `json:"group_id"`
		Title     string   `json:"title"`
		URL       string   `json:"url"`
		Notes     string   `json:"notes"`
		Tags      []string `json:"tags"`
		Favorite  bool     `json:"favorite"`
		Pinned    bool     `json:"pinned"`
		Archived  bool     `json:"archived"`
		SortOrder int      `json:"sort_order"`
		RemindAt  string   `json:"remind_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}

	if payload.GroupID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("group_id ist erforderlich"))
		return
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("title ist erforderlich"))
		return
	}
	urlValue := strings.TrimSpace(payload.URL)
	if err := validateURL(urlValue); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	remindAt, err := parseOptionalDateTime(payload.RemindAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("remind_at ist ungueltig"))
		return
	}

	res, err := app.db.ExecContext(
		r.Context(),
		`INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		payload.GroupID,
		title,
		urlValue,
		strings.TrimSpace(payload.Notes),
		serializeTags(payload.Tags),
		boolToInt(payload.Favorite),
		boolToInt(payload.Pinned),
		boolToInt(payload.Archived),
		payload.SortOrder,
		remindAt,
	)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (app *application) handleBookmarkRoutes(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/bookmarks/"), "/")
	bookmarkID, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || bookmarkID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige bookmark-id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload struct {
			GroupID   int64    `json:"group_id"`
			Title     string   `json:"title"`
			URL       string   `json:"url"`
			Notes     string   `json:"notes"`
			Tags      []string `json:"tags"`
			Favorite  bool     `json:"favorite"`
			Pinned    bool     `json:"pinned"`
			Archived  bool     `json:"archived"`
			SortOrder int      `json:"sort_order"`
			RemindAt  string   `json:"remind_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}

		if payload.GroupID <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("group_id ist erforderlich"))
			return
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("title ist erforderlich"))
			return
		}
		urlValue := strings.TrimSpace(payload.URL)
		if err := validateURL(urlValue); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		remindAt, err := parseOptionalDateTime(payload.RemindAt)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("remind_at ist ungueltig"))
			return
		}

		res, err := app.db.ExecContext(
			r.Context(),
			`UPDATE bookmarks
			 SET group_id = ?, title = ?, url = ?, notes = ?, tags = ?, favorite = ?, pinned = ?, archived = ?, sort_order = ?, remind_at = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?`,
			payload.GroupID,
			title,
			urlValue,
			strings.TrimSpace(payload.Notes),
			serializeTags(payload.Tags),
			boolToInt(payload.Favorite),
			boolToInt(payload.Pinned),
			boolToInt(payload.Archived),
			payload.SortOrder,
			remindAt,
			bookmarkID,
		)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("bookmark nicht gefunden"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		res, err := app.db.ExecContext(r.Context(), `DELETE FROM bookmarks WHERE id = ?`, bookmarkID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("bookmark nicht gefunden"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (app *application) reorderGroups(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		OrderedIDs []int64 `json:"ordered_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}

	for i, id := range payload.OrderedIDs {
		if id <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige gruppen-id"))
			return
		}
		if _, err := app.db.ExecContext(r.Context(), `UPDATE groups SET sort_order = ? WHERE id = ?`, i, id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (app *application) reorderBookmarksInGroup(w http.ResponseWriter, r *http.Request, groupID int64) {
	var payload struct {
		OrderedIDs []int64 `json:"ordered_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}

	for i, id := range payload.OrderedIDs {
		if id <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige bookmark-id"))
			return
		}
		if _, err := app.db.ExecContext(r.Context(), `UPDATE bookmarks SET sort_order = ? WHERE id = ? AND group_id = ?`, i, id, groupID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (app *application) listBookmarksByGroup(w http.ResponseWriter, r *http.Request, groupID int64) {
	rows, err := app.db.QueryContext(
		r.Context(),
		`SELECT id, group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at, created_at, updated_at
		 FROM bookmarks WHERE group_id = ? ORDER BY pinned DESC, sort_order ASC, id ASC`,
		groupID,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	items := make([]bookmark, 0)
	for rows.Next() {
		var b bookmark
		var tagsRaw string
		var favoriteInt int
		var pinnedInt int
		var archivedInt int
		if err := rows.Scan(&b.ID, &b.GroupID, &b.Title, &b.URL, &b.Notes, &tagsRaw, &favoriteInt, &pinnedInt, &archivedInt, &b.SortOrder, &b.RemindAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		b.Tags = parseTags(tagsRaw)
		b.Favorite = favoriteInt == 1
		b.Pinned = pinnedInt == 1
		b.Archived = archivedInt == 1
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"bookmarks": items})
}

func (app *application) fetchState(ctx context.Context) ([]group, error) {
	rows, err := app.db.QueryContext(
		ctx,
		`SELECT id, name, description, sort_order, created_at
		 FROM groups ORDER BY sort_order ASC, name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]group, 0)
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.SortOrder, &g.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range groups {
		bRows, err := app.db.QueryContext(
			ctx,
			`SELECT id, group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at, created_at, updated_at
			 FROM bookmarks WHERE group_id = ? ORDER BY pinned DESC, sort_order ASC, id ASC`,
			groups[i].ID,
		)
		if err != nil {
			return nil, err
		}

		items := make([]bookmark, 0)
		for bRows.Next() {
			var b bookmark
			var tagsRaw string
			var favoriteInt int
			var pinnedInt int
			var archivedInt int
			if err := bRows.Scan(&b.ID, &b.GroupID, &b.Title, &b.URL, &b.Notes, &tagsRaw, &favoriteInt, &pinnedInt, &archivedInt, &b.SortOrder, &b.RemindAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
				bRows.Close()
				return nil, err
			}
			b.Tags = parseTags(tagsRaw)
			b.Favorite = favoriteInt == 1
			b.Pinned = pinnedInt == 1
			b.Archived = archivedInt == 1
			items = append(items, b)
		}
		if err := bRows.Err(); err != nil {
			bRows.Close()
			return nil, err
		}
		bRows.Close()

		groups[i].Bookmarks = items
	}

	return groups, nil
}

func (app *application) handleMetalPrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	prices, err := app.getMetalPrices(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, prices)
}

func (app *application) handleSilverPrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	forceRefresh := r.URL.Query().Get("refresh") == "1"
	prices, err := app.getSilverPrices(r.Context(), forceRefresh)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, prices)
}

func (app *application) getSilverPrices(ctx context.Context, forceRefresh bool) (silverPricesPayload, error) {
	const cacheTTL = 2 * time.Hour

	app.silverPricesMu.RLock()
	if !forceRefresh && !app.silverPricesAt.IsZero() && time.Since(app.silverPricesAt) < cacheTTL {
		cached := app.silverPrices
		app.silverPricesMu.RUnlock()
		return cached, nil
	}
	app.silverPricesMu.RUnlock()

	fresh, err := fetchSilverPrices(ctx)
	if err != nil {
		return silverPricesPayload{}, err
	}
	if fresh.BestProduct == nil || fresh.BestProduct.Price <= 0 {
		app.silverPricesMu.RLock()
		if !app.silverPricesAt.IsZero() {
			cached := app.silverPrices
			app.silverPricesMu.RUnlock()
			return cached, nil
		}
		app.silverPricesMu.RUnlock()
		return fresh, nil
	}

	app.silverPricesMu.Lock()
	app.silverPrices = fresh
	app.silverPricesAt = fresh.FetchedAt
	app.silverPricesMu.Unlock()

	return fresh, nil
}

func (app *application) handleSilverPricesPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	page, err := fs.ReadFile(app.webFS, "silver-prices.html")
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("seite nicht gefunden"))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(page); err != nil {
		log.Printf("write silver-prices.html: %v", err)
	}
}

func (app *application) getMetalPrices(ctx context.Context) (metalPricesPayload, error) {
	const cacheTTL = 2 * time.Hour

	app.metalPricesMu.RLock()
	hasCache := !app.metalPricesAt.IsZero()
	cacheFresh := hasCache && time.Since(app.metalPricesAt) < cacheTTL
	if cacheFresh {
		cached := app.metalPrices
		cached.Cached = true
		cached.Stale = false
		app.metalPricesMu.RUnlock()
		return cached, nil
	}
	app.metalPricesMu.RUnlock()

	gold, silver, fetchErr := fetchBankPricesFromHomepage(ctx)

	now := time.Now().UTC()
	if fetchErr == nil {
		fresh := metalPricesPayload{
			GoldEURPerGram:   gold,
			SilverEURPerGram: silver,
			FetchedAt:        now,
			Cached:           false,
			Stale:            false,
		}

		app.metalPricesMu.Lock()
		app.metalPrices = fresh
		app.metalPricesAt = now
		app.metalPricesErr = ""
		app.metalPricesMu.Unlock()

		return fresh, nil
	}

	app.metalPricesMu.RLock()
	if !app.metalPricesAt.IsZero() {
		stale := app.metalPrices
		stale.Cached = true
		stale.Stale = true
		stale.LastError = fetchErr.Error()
		app.metalPricesMu.RUnlock()
		return stale, nil
	}
	app.metalPricesMu.RUnlock()

	return metalPricesPayload{}, fmt.Errorf("preise konnten nicht geladen werden: %s", fetchErr.Error())
}

func fetchSilverPrices(ctx context.Context) (silverPricesPayload, error) {
	const sourceURL = "https://www.edelmetall-handel.de/anlegen/silber?fine_weight%5B%5D=31%2C1+g+%281oz%29&ipp=100&sort=price_asc"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return silverPricesPayload{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return silverPricesPayload{}, fmt.Errorf("request fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return silverPricesPayload{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024))
	if err != nil {
		return silverPricesPayload{}, fmt.Errorf("response konnte nicht gelesen werden: %w", err)
	}

	products, err := parseSilverProductsFromHTML(string(body), sourceURL)
	if err != nil {
		return silverPricesPayload{
			BestProduct: &silverProduct{
				Name:      "Aktuelle Preisabfrage momentan nicht verfügbar",
				Category:  "Silber 1oz",
				Price:     0,
				PriceText: "n/a",
				URL:       sourceURL,
			},
			AllProducts: []silverProduct{},
			FetchedAt:   time.Now().UTC(),
			Source:      sourceURL,
		}, nil
	}

	sort.Slice(products, func(i, j int) bool {
		return products[i].Price < products[j].Price
	})

	limit := len(products)
	if limit > 20 {
		limit = 20
	}

	var bestProduct *silverProduct
	if len(products) > 0 {
		best := products[0]
		bestProduct = &best
	}

	return silverPricesPayload{
		BestProduct: bestProduct,
		AllProducts: products[:limit],
		FetchedAt:   time.Now().UTC(),
		Source:      sourceURL,
	}, nil
}

func parseSilverProductsFromHTML(htmlSource, source string) ([]silverProduct, error) {
	htmlSource = strings.ReplaceAll(htmlSource, "\n", " ")
	htmlSource = strings.ReplaceAll(htmlSource, "\r", " ")

	products := make([]silverProduct, 0)
	seen := make(map[string]struct{})

	productBlocksRe := regexp.MustCompile(`(?is)<product-item[^>]*>(.*?)</product-item>`)
	blocks := productBlocksRe.FindAllStringSubmatch(htmlSource, -1)
	if len(blocks) > 0 {
		for _, block := range blocks {
			blockHTML := block[1]
			anchorRe := regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
			anchorMatches := anchorRe.FindAllStringSubmatchIndex(blockHTML, -1)
			if len(anchorMatches) == 0 {
				continue
			}

			var name string
			var href string
			for _, match := range anchorMatches {
				candidateHref := blockHTML[match[2]:match[3]]
				candidateContent := blockHTML[match[4]:match[5]]
				candidateName := sanitizeHTMLText(candidateContent)
				if candidateName == "" {
					continue
				}
				name = candidateName
				href = candidateHref
				break
			}
			if name == "" || href == "" {
				continue
			}

			priceText, price, ok := findNearbyPrice(blockHTML, 0, len(blockHTML))
			if !ok || price <= 0 {
				continue
			}

			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			products = append(products, silverProduct{
				Name:      cleanProductName(name),
				Category:  "Silber 1oz",
				Price:     price,
				PriceText: priceText,
				URL:       normalizeProductURL(href),
			})
		}
	}

	if len(products) == 0 {
		anchorRe := regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
		anchorMatches := anchorRe.FindAllStringSubmatchIndex(htmlSource, -1)
		if len(anchorMatches) == 0 {
			return nil, fmt.Errorf("keine produkt-links gefunden in %s", source)
		}
		for _, match := range anchorMatches {
			anchorHTML := htmlSource[match[0]:match[1]]
			href := htmlSource[match[2]:match[3]]
			content := htmlSource[match[4]:match[5]]
			name := sanitizeHTMLText(content)
			if name == "" || !looksLike1ozSilverProduct(name, href, anchorHTML) {
				continue
			}
			priceText, price, ok := findNearbyPrice(htmlSource, match[0], match[1])
			if !ok || price <= 0 {
				continue
			}
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			products = append(products, silverProduct{
				Name:      cleanProductName(name),
				Category:  "Silber 1oz",
				Price:     price,
				PriceText: priceText,
				URL:       normalizeProductURL(href),
			})
		}
	}

	if len(products) == 0 {
		return nil, fmt.Errorf("keine 1oz-produkte gefunden in %s", source)
	}

	return products, nil
}

func looksLike1ozSilverProduct(name, href, anchorHTML string) bool {
	text := strings.ToLower(strings.TrimSpace(name + " " + href + " " + anchorHTML))
	if strings.Contains(text, "/anlegen/silber") || strings.Contains(text, "?fine_weight") || strings.Contains(text, "sort=") || strings.Contains(text, "price%5b") || strings.Contains(text, "/anlegen/silber/") {
		return false
	}
	if strings.Contains(text, "silbermünze") || strings.Contains(text, "silbermuenze") {
		return true
	}
	if strings.Contains(text, "silber") && strings.Contains(text, "1") && strings.Contains(text, "oz") {
		return true
	}
	if strings.Contains(text, "1 oz") || strings.Contains(text, "1oz") || strings.Contains(text, "31,1") || strings.Contains(text, "31,1g") || strings.Contains(text, "31,1 g") {
		return true
	}
	return false
}

func findNearbyPrice(htmlSource string, anchorStart, anchorEnd int) (string, float64, bool) {
	start := anchorStart
	if start < 0 {
		start = 0
	}
	end := anchorEnd + 20000
	if end > len(htmlSource) {
		end = len(htmlSource)
	}
	context := htmlSource[start:end]

	pricePatterns := []string{
		`(?is)<span[^>]*itemprop=["']price["'][^>]*content=["']([0-9.]+)["'][^>]*>`,
		`(?is)<span[^>]*content=["']([0-9.]+)["'][^>]*itemprop=["']price["'][^>]*>`,
		`(?is)<span[^>]*class=["'][^"']*money-price__amount[^"']*["'][^>]*>([^<]+)</span>`,
		`(?is)<span[^>]*itemprop=["']price["'][^>]*class=["'][^"']*money-price__amount[^"']*["'][^>]*>([^<]+)</span>`,
		`(?is)([0-9]{1,3}(?:[\.,][0-9]{3})*(?:[\.,][0-9]{2})?)\s*€`,
	}

	for _, pattern := range pricePatterns {
		priceRe := regexp.MustCompile(pattern)
		match := priceRe.FindStringSubmatch(context)
		if len(match) < 2 {
			continue
		}
		price, err := parsePriceString(match[1])
		if err == nil && price > 0 {
			return strings.TrimSpace(match[1]), price, true
		}
	}

	return "", 0, false
}

func cleanProductName(name string) string {
	normalized := strings.TrimSpace(name)
	normalized = strings.ReplaceAll(normalized, "- Hauptansicht", "")
	normalized = strings.ReplaceAll(normalized, "Hauptansicht", "")
	normalized = strings.TrimSpace(normalized)
	return normalized
}

func parsePriceString(raw string) (float64, error) {
	normalized := strings.TrimSpace(raw)
	normalized = strings.ReplaceAll(normalized, "€", "")
	normalized = strings.ReplaceAll(normalized, "\u00a0", "")
	if strings.Contains(normalized, ",") {
		normalized = strings.ReplaceAll(normalized, ".", "")
		normalized = strings.ReplaceAll(normalized, ",", ".")
	}
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return 0, fmt.Errorf("leerer preis")
	}
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func normalizeProductURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return "https://www.edelmetall-handel.de" + raw
	}
	return "https://www.edelmetall-handel.de/" + raw
}

func sanitizeHTMLText(raw string) string {
	text := strings.ReplaceAll(raw, "&nbsp;", " ")
	text = html.UnescapeString(text)
	text = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

func joinErrors(errs ...error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

func fetchBankPricesFromHomepage(ctx context.Context) (float64, float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.scheideanstalt.de/nc?header-prices-v2", nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/1.0 (+http://localhost)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("request fehlgeschlagen")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024))
	if err != nil {
		return 0, 0, fmt.Errorf("response konnte nicht gelesen werden")
	}

	html := string(body)
	if !strings.Contains(strings.ToLower(html), "bankpreis") {
		return 0, 0, fmt.Errorf("header-prices endpoint ohne bankpreis-daten")
	}

	gold, err := parseBankPriceEURPerGram(html, "Gold")
	if err != nil {
		return 0, 0, err
	}
	silver, err := parseBankPriceEURPerGram(html, "Silber")
	if err != nil {
		return 0, 0, err
	}

	return gold, silver, nil
}

func parseBankPriceEURPerGram(html, metalLabel string) (float64, error) {
	re := regexp.MustCompile(`(?is)` + regexp.QuoteMeta(metalLabel) + `\s*\(Bankpreis\)\s*</td>\s*<td[^>]*>\s*<span[^>]*>\s*([0-9]{1,3}(?:\.[0-9]{3})*(?:,[0-9]{2})?)\s*</span>\s*€\s*/\s*g\s*</td>\s*<td[^>]*>\s*<span[^>]*>\s*([0-9]{1,3}(?:\.[0-9]{3})*(?:,[0-9]{2})?)\s*</span>\s*€\s*/\s*g`)
	match := re.FindStringSubmatch(html)
	if len(match) != 3 {
		return 0, fmt.Errorf("%s: bankpreis nicht gefunden", strings.ToLower(metalLabel))
	}

	postPrice, err := parseGermanDecimal(match[1])
	if err != nil {
		return 0, fmt.Errorf("%s: postankaufpreis ungueltig", strings.ToLower(metalLabel))
	}
	switchPrice, err := parseGermanDecimal(match[2])
	if err != nil {
		return 0, fmt.Errorf("%s: schalterankaufpreis ungueltig", strings.ToLower(metalLabel))
	}

	if switchPrice > 0 {
		return switchPrice, nil
	}

	return postPrice, nil
}

func parseGermanDecimal(raw string) (float64, error) {
	normalized := strings.TrimSpace(raw)
	normalized = strings.ReplaceAll(normalized, ".", "")
	normalized = strings.ReplaceAll(normalized, ",", ".")
	return strconv.ParseFloat(normalized, 64)
}

func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url ist erforderlich")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("url ist ungueltig")
	}
	if u.Scheme == "" && strings.HasPrefix(u.Path, "/modules/") {
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url muss mit http:// oder https:// beginnen")
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}

	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, p := range parts {
		tag := strings.TrimSpace(p)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, tag)
	}
	return items
}

func serializeTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}

	items := make([]string, 0, len(tags))
	seen := make(map[string]struct{})
	for _, t := range tags {
		tag := strings.TrimSpace(t)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, tag)
	}

	return strings.Join(items, ",")
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiError{Error: err.Error()})
}

func (app *application) handleNotes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		var rows *sql.Rows
		var err error
		if q != "" {
			like := "%" + q + "%"
			rows, err = app.db.QueryContext(r.Context(),
				`SELECT id, title, content, type, bookmark_ids, tags, created_at, updated_at
				 FROM notes WHERE title LIKE ? OR content LIKE ? OR tags LIKE ?
				 ORDER BY updated_at DESC, id DESC`,
				like, like, like)
		} else {
			rows, err = app.db.QueryContext(r.Context(),
				`SELECT id, title, content, type, bookmark_ids, tags, created_at, updated_at
				 FROM notes ORDER BY updated_at DESC, id DESC`)
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		items := make([]note, 0)
		for rows.Next() {
			var n note
			var bmRaw, tagsRaw string
			if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.Type, &bmRaw, &tagsRaw, &n.CreatedAt, &n.UpdatedAt); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			n.BookmarkIDs = parseBookmarkIDs(bmRaw)
			n.Tags = parseTags(tagsRaw)
			items = append(items, n)
		}
		if err := rows.Err(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": items})

	case http.MethodPost:
		var payload struct {
			Title       string   `json:"title"`
			Content     string   `json:"content"`
			Type        string   `json:"type"`
			BookmarkIDs []int64  `json:"bookmark_ids"`
			Tags        []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("title ist erforderlich"))
			return
		}
		noteType := strings.TrimSpace(payload.Type)
		if noteType != "note" && noteType != "code" && noteType != "annotation" && noteType != "vault" {
			noteType = "note"
		}
		res, err := app.db.ExecContext(r.Context(),
			`INSERT INTO notes(title, content, type, bookmark_ids, tags) VALUES(?, ?, ?, ?, ?)`,
			title,
			strings.TrimSpace(payload.Content),
			noteType,
			serializeBookmarkIDs(payload.BookmarkIDs),
			serializeTags(payload.Tags),
		)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})

	default:
		methodNotAllowed(w)
	}
}

func (app *application) handleNoteRoutes(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/notes/"), "/")

	if trimmed == "bookmark-counts" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		app.handleNoteBookmarkCounts(w, r)
		return
	}

	if trimmed == "stats" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		app.handleNoteStats(w, r)
		return
	}

	noteID, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || noteID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige note-id"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		var n note
		var bmRaw, tagsRaw string
		err := app.db.QueryRowContext(r.Context(),
			`SELECT id, title, content, type, bookmark_ids, tags, created_at, updated_at FROM notes WHERE id = ?`, noteID).
			Scan(&n.ID, &n.Title, &n.Content, &n.Type, &bmRaw, &tagsRaw, &n.CreatedAt, &n.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("notiz nicht gefunden"))
			return
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		n.BookmarkIDs = parseBookmarkIDs(bmRaw)
		n.Tags = parseTags(tagsRaw)
		writeJSON(w, http.StatusOK, n)

	case http.MethodPut:
		var payload struct {
			Title       string   `json:"title"`
			Content     string   `json:"content"`
			Type        string   `json:"type"`
			BookmarkIDs []int64  `json:"bookmark_ids"`
			Tags        []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("title ist erforderlich"))
			return
		}
		noteType := strings.TrimSpace(payload.Type)
		if noteType != "note" && noteType != "code" && noteType != "annotation" && noteType != "vault" {
			noteType = "note"
		}
		res, err := app.db.ExecContext(r.Context(),
			`UPDATE notes SET title = ?, content = ?, type = ?, bookmark_ids = ?, tags = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			title,
			strings.TrimSpace(payload.Content),
			noteType,
			serializeBookmarkIDs(payload.BookmarkIDs),
			serializeTags(payload.Tags),
			noteID,
		)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("notiz nicht gefunden"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case http.MethodDelete:
		res, err := app.db.ExecContext(r.Context(), `DELETE FROM notes WHERE id = ?`, noteID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("notiz nicht gefunden"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		methodNotAllowed(w)
	}
}

func (app *application) handleNoteStats(w http.ResponseWriter, r *http.Request) {
	rows, err := app.db.QueryContext(r.Context(), `SELECT type, COUNT(*) FROM notes GROUP BY type`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	typeCounts := make(map[string]int64)
	var totalNotes int64
	for rows.Next() {
		var typ string
		var count int64
		if err := rows.Scan(&typ, &count); err != nil {
			continue
		}
		typeCounts[typ] = count
		totalNotes += count
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	tagRows, err := app.db.QueryContext(r.Context(), `SELECT tags FROM notes WHERE tags != ''`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer tagRows.Close()

	tagCounts := make(map[string]int)
	for tagRows.Next() {
		var raw string
		if err := tagRows.Scan(&raw); err != nil {
			continue
		}
		for _, t := range parseTags(raw) {
			tagCounts[t]++
		}
	}
	if err := tagRows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	type tagCount struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	tags := make([]tagCount, 0, len(tagCounts))
	for tag, count := range tagCounts {
		tags = append(tags, tagCount{tag, count})
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Count > tags[j].Count
	})

	if len(tags) > 20 {
		tags = tags[:20]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_notes": totalNotes,
		"type_counts": typeCounts,
		"top_tags":    tags,
	})
}

func (app *application) handleNoteBookmarkCounts(w http.ResponseWriter, r *http.Request) {
	rows, err := app.db.QueryContext(r.Context(), `SELECT bookmark_ids FROM notes WHERE bookmark_ids != ''`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		for _, id := range parseBookmarkIDs(raw) {
			counts[strconv.FormatInt(id, 10)]++
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts})
}

func (app *application) removeBookmarkFromNotes(ctx context.Context, bookmarkID int64) {
	idStr := strconv.FormatInt(bookmarkID, 10)
	rows, err := app.db.QueryContext(ctx,
		`SELECT id, bookmark_ids FROM notes WHERE bookmark_ids LIKE ?`,
		"%"+idStr+"%")
	if err != nil {
		log.Printf("removeBookmarkFromNotes: %v", err)
		return
	}
	defer rows.Close()

	type noteUpdate struct {
		id  int64
		ids string
	}
	var updates []noteUpdate
	for rows.Next() {
		var nid int64
		var raw string
		if err := rows.Scan(&nid, &raw); err != nil {
			continue
		}
		filtered := make([]int64, 0)
		for _, id := range parseBookmarkIDs(raw) {
			if id != bookmarkID {
				filtered = append(filtered, id)
			}
		}
		updates = append(updates, noteUpdate{id: nid, ids: serializeBookmarkIDs(filtered)})
	}
	rows.Close()
	for _, u := range updates {
		if _, err := app.db.ExecContext(ctx, `UPDATE notes SET bookmark_ids = ? WHERE id = ?`, u.ids, u.id); err != nil {
			log.Printf("removeBookmarkFromNotes update %d: %v", u.id, err)
		}
	}
}

func serializeBookmarkIDs(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	seen := make(map[int64]struct{})
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

func parseBookmarkIDs(raw string) []int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []int64{}
	}
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode json: %v", err)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic in request %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "interner serverfehler", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func parseOptionalPositiveInt64(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return id, nil
}

func filterGroupsByID(groups []group, groupID int64) []group {
	out := make([]group, 0, 1)
	for _, g := range groups {
		if g.ID == groupID {
			out = append(out, g)
			break
		}
	}
	return out
}

func parseOptionalDateTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}

	return nil, fmt.Errorf("invalid datetime")
}

func formatOptionalDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func writeCSVExport(w io.Writer, groups []group) error {
	cw := csv.NewWriter(w)
	header := []string{
		"group_id",
		"group_name",
		"group_description",
		"bookmark_id",
		"title",
		"url",
		"notes",
		"tags",
		"favorite",
		"pinned",
		"archived",
		"sort_order",
		"remind_at",
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, g := range groups {
		for _, b := range g.Bookmarks {
			record := []string{
				strconv.FormatInt(g.ID, 10),
				g.Name,
				g.Description,
				strconv.FormatInt(b.ID, 10),
				b.Title,
				b.URL,
				b.Notes,
				strings.Join(b.Tags, ","),
				strconv.FormatBool(b.Favorite),
				strconv.FormatBool(b.Pinned),
				strconv.FormatBool(b.Archived),
				strconv.Itoa(b.SortOrder),
				formatOptionalDate(b.RemindAt),
			}
			if err := cw.Write(record); err != nil {
				return err
			}
		}
	}

	cw.Flush()
	return cw.Error()
}

func writeHTMLExport(w io.Writer, groups []group) error {
	tpl := `<!doctype html>
<html lang="de">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Lesezeichen Startseite</title>
  <style>
    :root { --bg:#f6f7fb; --ink:#142033; --muted:#5a687d; --card:#fff; --line:#dbe2ee; --accent:#0f8e74; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: "Segoe UI", "Avenir Next", sans-serif; background: var(--bg); color: var(--ink); }
    .wrap { max-width: 1200px; margin: 0 auto; padding: 24px 16px 40px; }
    h1 { margin: 0 0 6px; }
    .hint { margin: 0 0 20px; color: var(--muted); }
    .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 12px; }
    .group { background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 12px; }
    .group h2 { margin: 0; font-size: 1.08rem; }
    .desc { margin: 4px 0 10px; color: var(--muted); font-size: 0.9rem; }
    ul { margin: 0; padding-left: 18px; display: grid; gap: 6px; }
    a { color: var(--accent); text-decoration: none; }
    a:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <main class="wrap">
    <h1>Lesezeichen Startseite</h1>
    <p class="hint">Generiert am {{ .GeneratedAt }}</p>
    <section class="grid">
      {{ range .Groups }}
      <article class="group">
        <h2>{{ .Name }}</h2>
        {{ if .Description }}<p class="desc">{{ .Description }}</p>{{ end }}
        <ul>
          {{ range .Bookmarks }}
          <li><a href="{{ .URL }}" target="_blank" rel="noreferrer">{{ .Title }}</a></li>
          {{ end }}
        </ul>
      </article>
      {{ end }}
    </section>
  </main>
</body>
</html>`

	t, err := template.New("export").Parse(tpl)
	if err != nil {
		return err
	}

	data := struct {
		GeneratedAt string
		Groups      []group
	}{
		GeneratedAt: time.Now().Format("02.01.2006 15:04"),
		Groups:      groups,
	}

	return t.Execute(w, data)
}
