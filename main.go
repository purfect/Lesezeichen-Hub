package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type application struct {
	db             *sql.DB
	metalPricesMu  sync.RWMutex
	metalPrices    metalPricesPayload
	metalPricesAt  time.Time
	metalPricesErr string
}

type metalPricesPayload struct {
	GoldEURPerGram   float64   `json:"gold_eur_per_g"`
	SilverEURPerGram float64   `json:"silver_eur_per_g"`
	FetchedAt        time.Time `json:"fetched_at"`
	Cached           bool      `json:"cached"`
	Stale            bool      `json:"stale"`
	LastError        string    `json:"last_error,omitempty"`
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

	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		log.Fatalf("read embedded index.html: %v", err)
	}

	mux.HandleFunc("/api/state", app.handleState)
	mux.HandleFunc("/api/export", app.handleExport)
	mux.HandleFunc("/api/import", app.handleImport)
	mux.HandleFunc("/api/groups", app.handleGroups)
	mux.HandleFunc("/api/groups/", app.handleGroupRoutes)
	mux.HandleFunc("/api/bookmarks", app.handleBookmarks)
	mux.HandleFunc("/api/bookmarks/", app.handleBookmarkRoutes)
	mux.HandleFunc("/api/metal-prices", app.handleMetalPrices)
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
