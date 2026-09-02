package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
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

const (
	githubOwner       = "purfect"
	githubRepo        = "Lesezeichen-Hub"
	moduleGithubOwner = "Lesezeichen-Hub"
	githubAPIBase     = "https://api.github.com"
	githubWebBase     = "https://github.com"
)

var appVersion = "dev"

type application struct {
	db               *sql.DB
	webFS            fs.FS
	moduleAPIBase    string
	moduleWebBase    string
	moduleInstallDir string
	externalPrices   bool
	metalPricesMu    sync.RWMutex
	metalPrices      metalPricesPayload
	metalPricesAt    time.Time
	metalPricesErr   string
	silverPricesMu   sync.RWMutex
	silverPrices     silverPricesPayload
	silverPricesAt   time.Time
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

type localModule struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	URL       string `json:"url"`
	Available bool   `json:"available"`
	Managed   bool   `json:"managed"`
	Error     string `json:"error,omitempty"`
}

type catalogModule struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	RepositoryURL string `json:"repository_url"`
	DefaultBranch string `json:"default_branch"`
	Installed     bool   `json:"installed"`
	LocalID       int64  `json:"local_id,omitempty"`
	LocalURL      string `json:"local_url,omitempty"`
}

type githubRepository struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
}

type apiError struct {
	Error string `json:"error"`
}

type updateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	AssetName       string `json:"asset_name,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	AssetURL        string `json:"asset_url,omitempty"`
	CanInstall      bool   `json:"can_install"`
	Message         string `json:"message,omitempty"`
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	HTMLURL string               `json:"html_url"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
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

type restorePreview struct {
	Valid                bool     `json:"valid"`
	Errors               []string `json:"errors"`
	NewGroups            int      `json:"new_groups"`
	ExistingGroups       int      `json:"existing_groups"`
	NewBookmarks         int      `json:"new_bookmarks"`
	ConflictingBookmarks int      `json:"conflicting_bookmarks"`
	NewNotes             int      `json:"new_notes"`
	ConflictingNotes     int      `json:"conflicting_notes"`
	Conflicts            []string `json:"conflicts"`
}

//go:embed web/*
var embeddedWebFiles embed.FS

func main() {
	dbPath := envOrDefault("BOOKMARK_DB_PATH", "./data.db")
	addr := envOrDefault("ADDR", "127.0.0.1:2222")

	db, err := sql.Open("sqlite", databaseDSN(dbPath))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := initializeSchema(db); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	app := &application{
		db:               db,
		moduleAPIBase:    githubAPIBase,
		moduleWebBase:    githubWebBase,
		moduleInstallDir: envOrDefault("MODULES_PATH", "./modules"),
		externalPrices:   envBool("ENABLE_EXTERNAL_PRICES", true),
	}
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
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/update/check", app.handleUpdateCheck)
	mux.HandleFunc("/api/update/install", app.handleUpdateInstall)
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
	mux.HandleFunc("/api/modules/", app.handleModuleRoutes)
	mux.HandleFunc("/api/module-catalog", app.handleModuleCatalog)
	mux.HandleFunc("/api/module-catalog/", app.handleModuleCatalogRoutes)
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

	log.Printf("Lesezeichen-Server laeuft auf http://%s", addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}

func (app *application) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		app.listModules(w, r)
		return
	}
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
	rootPath, err := resolveModuleRoot(rootPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
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
		if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks SET group_id = ?, title = ?, notes = ?, tags = ?, favorite = 0, archived = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, payload.GroupID, name, strings.TrimSpace(payload.Notes), serializeTags(payload.Tags), existingBookmarkID); err != nil {
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
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, sort_order) VALUES(?, ?, ?, ?, ?, ?, ?)`, payload.GroupID, name, moduleURL, strings.TrimSpace(payload.Notes), serializeTags(payload.Tags), 0, 0); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("modulstart konnte nicht angelegt werden: %v", err))
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": moduleID, "url": moduleURL})
}

func (app *application) listModules(w http.ResponseWriter, r *http.Request) {
	rows, err := app.db.QueryContext(r.Context(), `SELECT id, name, root_path, managed FROM modules ORDER BY lower(name), id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	modules := make([]localModule, 0)
	for rows.Next() {
		var item localModule
		if err := rows.Scan(&item.ID, &item.Name, &item.Path, &item.Managed); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		item.URL = fmt.Sprintf("/modules/%d/index.html", item.ID)
		if _, err := resolveModuleRoot(item.Path); err != nil {
			item.Error = err.Error()
		} else {
			item.Available = true
		}
		modules = append(modules, item)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": modules})
}

func (app *application) handleModuleCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	modules, err := app.fetchModuleCatalog(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": modules})
}

func (app *application) handleModuleCatalogRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/module-catalog/"), "/")
	if !strings.HasSuffix(trimmed, "/install") {
		http.NotFound(w, r)
		return
	}
	repositoryName, err := url.PathUnescape(strings.TrimSuffix(trimmed, "/install"))
	if err != nil || repositoryName == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiger repository-name"))
		return
	}

	module, err := app.installCatalogModule(r.Context(), repositoryName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, module)
}

func (app *application) fetchModuleCatalog(ctx context.Context) ([]catalogModule, error) {
	apiBase := strings.TrimRight(app.moduleAPIBase, "/")
	if apiBase == "" {
		apiBase = githubAPIBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/orgs/"+moduleGithubOwner+"/repos?per_page=100&type=public", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("modulkatalog konnte nicht geladen werden: %w", err)
	}
	defer response.Body.Close()
	var repositories []githubRepository
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		repositories, err = app.fetchModuleRepositoriesFromWeb(ctx)
		if err != nil {
			return nil, fmt.Errorf("GitHub-API-Limit erreicht und Ersatzliste konnte nicht geladen werden: %w", err)
		}
	} else if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub antwortet mit Status %d", response.StatusCode)
	} else if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&repositories); err != nil {
		return nil, fmt.Errorf("ungueltige GitHub-Antwort: %w", err)
	}
	installed := make(map[string]localModule)
	rows, err := app.db.QueryContext(ctx, `SELECT id, name FROM modules`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item localModule
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			rows.Close()
			return nil, err
		}
		installed[strings.ToLower(item.Name)] = item
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	modules := make([]catalogModule, 0, len(repositories))
	for _, repository := range repositories {
		if repository.Archived || repository.Fork || repository.Name == "" || strings.HasPrefix(repository.Name, ".") || repository.DefaultBranch == "" {
			continue
		}
		module := catalogModule{
			Name:          repository.Name,
			Description:   repository.Description,
			RepositoryURL: repository.HTMLURL,
			DefaultBranch: repository.DefaultBranch,
		}
		if local, ok := installed[strings.ToLower(repository.Name)]; ok {
			module.Installed = true
			module.LocalID = local.ID
			module.LocalURL = fmt.Sprintf("/modules/%d/index.html", local.ID)
		}
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return strings.ToLower(modules[i].Name) < strings.ToLower(modules[j].Name) })
	return modules, nil
}

func (app *application) fetchModuleRepositoriesFromWeb(ctx context.Context) ([]githubRepository, error) {
	webBase := strings.TrimRight(app.moduleWebBase, "/")
	if webBase == "" {
		webBase = githubWebBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webBase+"/orgs/"+moduleGithubOwner+"/repositories?type=all", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub-Webseite antwortet mit Status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	pattern := regexp.MustCompile(`href="/` + regexp.QuoteMeta(moduleGithubOwner) + `/([A-Za-z0-9_.-]+)"`)
	seen := make(map[string]bool)
	repositories := make([]githubRepository, 0)
	for _, match := range pattern.FindAllSubmatch(body, -1) {
		name := string(match[1])
		if seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		repositories = append(repositories, githubRepository{
			Name:          name,
			HTMLURL:       webBase + "/" + moduleGithubOwner + "/" + name,
			DefaultBranch: "main",
		})
	}
	if len(repositories) == 0 {
		return nil, fmt.Errorf("keine Repositorys gefunden")
	}
	return repositories, nil
}

func (app *application) installCatalogModule(ctx context.Context, repositoryName string) (catalogModule, error) {
	modules, err := app.fetchModuleCatalog(ctx)
	if err != nil {
		return catalogModule{}, err
	}
	var selected catalogModule
	for _, module := range modules {
		if strings.EqualFold(module.Name, repositoryName) {
			selected = module
			break
		}
	}
	if selected.Name == "" {
		return catalogModule{}, fmt.Errorf("repository gehoert nicht zum verfuegbaren Modulkatalog")
	}
	if selected.Installed {
		return catalogModule{}, fmt.Errorf("modul ist bereits eingerichtet")
	}

	installBase := strings.TrimSpace(app.moduleInstallDir)
	if installBase == "" {
		installBase = "./modules"
	}
	installBase, err = filepath.Abs(installBase)
	if err != nil {
		return catalogModule{}, fmt.Errorf("modulordner ist ungueltig")
	}
	if err := os.MkdirAll(installBase, 0755); err != nil {
		return catalogModule{}, fmt.Errorf("modulordner konnte nicht erstellt werden: %w", err)
	}
	destination := filepath.Join(installBase, selected.Name)
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return catalogModule{}, fmt.Errorf("zielordner fuer das modul existiert bereits")
	}

	archivePath, err := app.downloadCatalogModuleArchive(ctx, selected, installBase)
	if err != nil {
		return catalogModule{}, err
	}
	defer os.Remove(archivePath)

	staging, err := os.MkdirTemp(installBase, ".module-install-*")
	if err != nil {
		return catalogModule{}, err
	}
	defer os.RemoveAll(staging)
	if err := extractModuleArchive(archivePath, staging); err != nil {
		return catalogModule{}, err
	}
	moduleRoot, err := findModuleRoot(staging)
	if err != nil {
		return catalogModule{}, err
	}
	rootRelative, err := filepath.Rel(staging, moduleRoot)
	if err != nil {
		return catalogModule{}, err
	}
	if err := os.Rename(staging, destination); err != nil {
		return catalogModule{}, fmt.Errorf("modul konnte nicht eingerichtet werden: %w", err)
	}
	moduleRoot = filepath.Join(destination, rootRelative)
	removeDestination := true
	defer func() {
		if removeDestination {
			_ = os.RemoveAll(destination)
		}
	}()

	tx, err := app.db.BeginTx(ctx, nil)
	if err != nil {
		return catalogModule{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO groups(name, description) VALUES('Module', 'Installierte Module')`); err != nil {
		return catalogModule{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO modules(name, root_path, managed) VALUES(?, ?, 1)`, selected.Name, moduleRoot)
	if err != nil {
		return catalogModule{}, fmt.Errorf("modul konnte nicht registriert werden: %w", err)
	}
	moduleID, _ := result.LastInsertId()
	moduleURL := fmt.Sprintf("/modules/%d/index.html", moduleID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO bookmarks(group_id, title, url, notes, favorite, sort_order) SELECT id, ?, ?, ?, 0, 0 FROM groups WHERE name = 'Module'`, selected.Name, moduleURL, selected.Description); err != nil {
		return catalogModule{}, fmt.Errorf("modulstart konnte nicht angelegt werden: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalogModule{}, err
	}
	removeDestination = false
	selected.Installed = true
	selected.LocalID = moduleID
	selected.LocalURL = moduleURL
	return selected, nil
}

func (app *application) updateCatalogModule(ctx context.Context, moduleID int64) (catalogModule, error) {
	var moduleName, moduleRoot string
	var managed bool
	if err := app.db.QueryRowContext(ctx, `SELECT name, root_path, managed FROM modules WHERE id = ?`, moduleID).Scan(&moduleName, &moduleRoot, &managed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return catalogModule{}, fmt.Errorf("modul nicht gefunden")
		}
		return catalogModule{}, err
	}
	if !managed {
		return catalogModule{}, fmt.Errorf("nur aus dem Katalog eingerichtete Module koennen aktualisiert werden")
	}

	installBase := strings.TrimSpace(app.moduleInstallDir)
	if installBase == "" {
		installBase = "./modules"
	}
	installBase, err := filepath.Abs(installBase)
	if err != nil {
		return catalogModule{}, fmt.Errorf("modulordner ist ungueltig")
	}
	if err := os.MkdirAll(installBase, 0755); err != nil {
		return catalogModule{}, fmt.Errorf("modulordner konnte nicht erstellt werden: %w", err)
	}

	currentTopLevel, err := managedModuleTopLevel(installBase, moduleRoot)
	if err != nil {
		return catalogModule{}, err
	}
	repositoryName := filepath.Base(currentTopLevel)
	modules, err := app.fetchModuleCatalog(ctx)
	if err != nil {
		return catalogModule{}, err
	}
	var selected catalogModule
	for _, module := range modules {
		if strings.EqualFold(module.Name, moduleName) || strings.EqualFold(module.Name, repositoryName) {
			selected = module
			break
		}
	}
	if selected.Name == "" {
		return catalogModule{}, fmt.Errorf("repository gehoert nicht zum verfuegbaren Modulkatalog")
	}
	archivePath, err := app.downloadCatalogModuleArchive(ctx, selected, installBase)
	if err != nil {
		return catalogModule{}, err
	}
	defer os.Remove(archivePath)

	staging, err := os.MkdirTemp(installBase, ".module-update-*")
	if err != nil {
		return catalogModule{}, err
	}
	removeStaging := true
	defer func() {
		if removeStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := extractModuleArchive(archivePath, staging); err != nil {
		return catalogModule{}, err
	}
	newRoot, err := findModuleRoot(staging)
	if err != nil {
		return catalogModule{}, err
	}
	rootRelative, err := filepath.Rel(staging, newRoot)
	if err != nil {
		return catalogModule{}, err
	}

	backup := filepath.Join(installBase, fmt.Sprintf(".module-backup-%d-%d", moduleID, time.Now().UnixNano()))
	hasBackup := false
	if _, err := os.Stat(currentTopLevel); err == nil {
		if err := os.Rename(currentTopLevel, backup); err != nil {
			return catalogModule{}, fmt.Errorf("bestehendes modul konnte nicht vorbereitet werden: %w", err)
		}
		hasBackup = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return catalogModule{}, fmt.Errorf("bestehendes modul konnte nicht geprueft werden: %w", err)
	}

	replaceOK := false
	defer func() {
		if replaceOK {
			if hasBackup {
				_ = os.RemoveAll(backup)
			}
			return
		}
		_ = os.RemoveAll(currentTopLevel)
		if hasBackup {
			_ = os.Rename(backup, currentTopLevel)
		}
	}()
	if err := os.Rename(staging, currentTopLevel); err != nil {
		return catalogModule{}, fmt.Errorf("modul konnte nicht aktualisiert werden: %w", err)
	}
	removeStaging = false
	updatedRoot := filepath.Join(currentTopLevel, rootRelative)
	if _, err := app.db.ExecContext(ctx, `UPDATE modules SET root_path = ? WHERE id = ?`, updatedRoot, moduleID); err != nil {
		return catalogModule{}, err
	}
	replaceOK = true

	selected.Installed = true
	selected.LocalID = moduleID
	selected.LocalURL = fmt.Sprintf("/modules/%d/index.html", moduleID)
	return selected, nil
}

func (app *application) downloadCatalogModuleArchive(ctx context.Context, module catalogModule, installBase string) (string, error) {
	archive, err := os.CreateTemp(installBase, ".module-*.zip")
	if err != nil {
		return "", err
	}
	archivePath := archive.Name()
	archive.Close()
	archiveURL := ""
	if strings.TrimSpace(app.moduleAPIBase) != "" && !strings.EqualFold(strings.TrimRight(app.moduleAPIBase, "/"), githubAPIBase) {
		archiveURL = fmt.Sprintf("%s/repos/%s/%s/zipball/%s", strings.TrimRight(app.moduleAPIBase, "/"), moduleGithubOwner, url.PathEscape(module.Name), url.PathEscape(module.DefaultBranch))
	} else {
		archiveURL = fmt.Sprintf("%s/archive/refs/heads/%s.zip", strings.TrimRight(module.RepositoryURL, "/"), url.PathEscape(module.DefaultBranch))
	}
	if err := downloadFile(ctx, archiveURL, archivePath); err != nil {
		_ = os.Remove(archivePath)
		return "", err
	}
	return archivePath, nil
}

func extractModuleArchive(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("modularchiv ist ungueltig: %w", err)
	}
	defer reader.Close()
	var totalSize uint64
	for _, entry := range reader.File {
		parts := strings.Split(filepath.ToSlash(entry.Name), "/")
		if len(parts) < 2 {
			continue
		}
		relativeName := path.Clean(strings.Join(parts[1:], "/"))
		if relativeName == "." {
			continue
		}
		if relativeName == ".." || strings.HasPrefix(relativeName, "../") || path.IsAbs(relativeName) || entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("modularchiv enthaelt einen unzulaessigen Pfad")
		}
		totalSize += entry.UncompressedSize64
		if totalSize > 500<<20 {
			return fmt.Errorf("modularchiv ist zu gross")
		}
		target := filepath.Join(destination, filepath.FromSlash(relativeName))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(targetFile, source)
		closeErr := targetFile.Close()
		source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func findModuleRoot(root string) (string, error) {
	for _, relative := range []string{"", "public", "static", "web"} {
		candidate := filepath.Join(root, relative)
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("repository enthaelt keine unterstuetzte index.html")
}

func (app *application) removeManagedModuleFiles(moduleRoot string) {
	installBase := strings.TrimSpace(app.moduleInstallDir)
	if installBase == "" {
		installBase = "./modules"
	}
	installBase, err := filepath.Abs(installBase)
	if err != nil {
		return
	}
	moduleRoot, err = filepath.Abs(moduleRoot)
	if err != nil {
		return
	}
	relative, err := filepath.Rel(installBase, moduleRoot)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return
	}
	topLevel := strings.Split(relative, string(filepath.Separator))[0]
	_ = os.RemoveAll(filepath.Join(installBase, topLevel))
}

func managedModuleTopLevel(installBase, moduleRoot string) (string, error) {
	installBase, err := filepath.Abs(installBase)
	if err != nil {
		return "", fmt.Errorf("modulordner ist ungueltig")
	}
	moduleRoot, err = filepath.Abs(moduleRoot)
	if err != nil {
		return "", fmt.Errorf("modulpfad ist ungueltig")
	}
	relative, err := filepath.Rel(installBase, moduleRoot)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("modul liegt nicht im verwalteten modulordner")
	}
	topLevel := strings.Split(relative, string(filepath.Separator))[0]
	return filepath.Join(installBase, topLevel), nil
}

func (app *application) handleModuleRoutes(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/modules/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungültige modul-id"))
		return
	}
	moduleID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || moduleID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungültige modul-id"))
		return
	}
	if len(parts) == 2 && parts[1] == "update" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		module, err := app.updateCatalogModule(r.Context(), moduleID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, module)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}

	moduleURL := fmt.Sprintf("/modules/%d/index.html", moduleID)
	switch r.Method {
	case http.MethodPut:
		var payload struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungültiges JSON"))
			return
		}
		name := strings.TrimSpace(payload.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("name ist erforderlich"))
			return
		}
		rootPath, err := resolveModuleRoot(payload.Path)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}

		tx, err := app.db.BeginTx(r.Context(), nil)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer tx.Rollback()
		result, err := tx.ExecContext(r.Context(), `UPDATE modules SET name = ?, root_path = ? WHERE id = ?`, name, rootPath, moduleID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("modul nicht gefunden"))
			return
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks SET title = ?, updated_at = CURRENT_TIMESTAMP WHERE url = ?`, name, moduleURL); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if err := tx.Commit(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": rootPath})
	case http.MethodDelete:
		var moduleRoot string
		var managed bool
		if err := app.db.QueryRowContext(r.Context(), `SELECT root_path, managed FROM modules WHERE id = ?`, moduleID).Scan(&moduleRoot, &managed); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, fmt.Errorf("modul nicht gefunden"))
			} else {
				writeErr(w, http.StatusInternalServerError, err)
			}
			return
		}
		rows, err := app.db.QueryContext(r.Context(), `SELECT id FROM bookmarks WHERE url = ?`, moduleURL)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		bookmarkIDs := make([]int64, 0)
		for rows.Next() {
			var bookmarkID int64
			if err := rows.Scan(&bookmarkID); err != nil {
				rows.Close()
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			bookmarkIDs = append(bookmarkIDs, bookmarkID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		rows.Close()

		tx, err := app.db.BeginTx(r.Context(), nil)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks WHERE url = ?`, moduleURL); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		result, err := tx.ExecContext(r.Context(), `DELETE FROM modules WHERE id = ?`, moduleID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("modul nicht gefunden"))
			return
		}
		if err := tx.Commit(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		for _, bookmarkID := range bookmarkIDs {
			app.removeBookmarkFromNotes(r.Context(), bookmarkID)
		}
		if managed {
			app.removeManagedModuleFiles(moduleRoot)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
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
	if relativePath == "." {
		relativePath = "index.html"
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, "../") || path.IsAbs(relativePath) {
		http.NotFound(w, r)
		return
	}

	fullPath, err := resolveModuleFile(rootPath, relativePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	// ServeContent statt ServeFile: ServeFile beantwortet .../index.html mit einem 301, den Browser dauerhaft zwischenspeichern.
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func resolveModuleRoot(rootPath string) (string, error) {
	absolutePath, err := filepath.Abs(strings.TrimSpace(rootPath))
	if err != nil {
		return "", fmt.Errorf("pfad ist ungültig")
	}
	canonicalPath, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return "", fmt.Errorf("modulpfad ist kein vorhandener ordner")
	}
	info, err := os.Stat(canonicalPath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("modulpfad ist kein vorhandener ordner")
	}
	if indexInfo, err := os.Stat(filepath.Join(canonicalPath, "index.html")); err != nil || indexInfo.IsDir() {
		return "", fmt.Errorf("modul braucht eine index.html")
	}
	return canonicalPath, nil
}

func resolveModuleFile(rootPath, relativePath string) (string, error) {
	canonicalRoot, err := resolveModuleRoot(rootPath)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(canonicalRoot, filepath.FromSlash(relativePath))
	if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
		candidate = filepath.Join(candidate, "index.html")
	}
	canonicalCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	relativeToRoot, err := filepath.Rel(canonicalRoot, canonicalCandidate)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeToRoot) {
		return "", fmt.Errorf("moduldatei liegt außerhalb des modulordners")
	}
	return canonicalCandidate, nil
}

func initializeSchema(db *sql.DB) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`PRAGMA synchronous = NORMAL;`,
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}

	statements := []string{
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
			managed INTEGER NOT NULL DEFAULT 0,
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
		`ALTER TABLE modules ADD COLUMN managed INTEGER NOT NULL DEFAULT 0`,
	}

	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_sort ON bookmarks(group_id, pinned DESC, sort_order ASC, id ASC);`); err != nil {
		return err
	}

	// Ohne aktive Fremdschluessel liess das Loeschen einer Gruppe deren Lesezeichen unsichtbar zurueck.
	if _, err := db.Exec(`DELETE FROM bookmarks WHERE group_id NOT IN (SELECT id FROM groups)`); err != nil {
		return err
	}

	return nil
}

// PRAGMA-Einstellungen gelten pro Verbindung und muessen daher aus der DSN kommen.
func databaseDSN(path string) string {
	if strings.Contains(path, "?") {
		return path + "&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	return path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
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

func (app *application) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"external_prices": app.externalPrices, "version": appVersion})
}

func (app *application) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	info, err := fetchLatestUpdateInfo(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, info)
}

func (app *application) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if runtime.GOOS != "windows" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("updates koennen nur unter Windows installiert werden"))
		return
	}

	info, err := fetchLatestUpdateInfo(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if !info.UpdateAvailable {
		writeJSON(w, http.StatusOK, map[string]any{"message": "keine neue version verfuegbar"})
		return
	}
	if !info.CanInstall {
		writeErr(w, http.StatusBadRequest, errors.New(info.Message))
		return
	}

	currentExe, err := os.Executable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("pfad der laufenden exe konnte nicht ermittelt werden"))
		return
	}
	currentExe, err = filepath.Abs(currentExe)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("pfad der laufenden exe ist ungueltig"))
		return
	}
	if !strings.EqualFold(filepath.Ext(currentExe), ".exe") {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("update-installation ist nur aus einer windows-exe moeglich"))
		return
	}

	appDir := filepath.Dir(currentExe)
	runtimeDir := filepath.Join(appDir, ".runtime")
	updatesDir := filepath.Join(runtimeDir, "updates")
	if err := os.MkdirAll(updatesDir, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("update-ordner konnte nicht erstellt werden: %w", err))
		return
	}

	targetPath := filepath.Join(updatesDir, info.AssetName)
	if err := downloadFile(r.Context(), info.AssetURL, targetPath); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := verifyDownloadedAsset(r.Context(), info.AssetURL, targetPath); err != nil {
		_ = os.Remove(targetPath)
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	addr := strings.TrimSpace(r.Host)
	if addr == "" {
		addr = envOrDefault("ADDR", "127.0.0.1:2222")
	}

	scriptPath := filepath.Join(runtimeDir, "install-update.ps1")
	if err := writeUpdateScript(scriptPath); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	cmd := exec.Command("powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
		"-TargetExe", currentExe,
		"-NewExe", targetPath,
		"-AppDir", appDir,
		"-RuntimeDir", runtimeDir,
		"-Address", addr,
		"-OldPid", strconv.Itoa(os.Getpid()),
	)
	if err := cmd.Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("updater konnte nicht gestartet werden: %w", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"message":        "update wird installiert; der hub startet gleich neu",
		"latest_version": info.LatestVersion,
	})
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
	preview, err := app.buildRestorePreview(r.Context(), payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if r.URL.Query().Get("preview") == "1" {
		writeJSON(w, http.StatusOK, preview)
		return
	}
	if !preview.Valid {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("backup ist ungültig: %s", strings.Join(preview.Errors, "; ")))
		return
	}
	conflictStrategy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("conflicts")))
	if conflictStrategy == "" {
		conflictStrategy = "overwrite"
	}
	if conflictStrategy != "overwrite" && conflictStrategy != "skip" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungültige konfliktstrategie"))
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
		} else if conflictStrategy == "overwrite" {
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
			} else if conflictStrategy == "overwrite" {
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
		} else if conflictStrategy == "overwrite" {
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
		"conflict_strategy": conflictStrategy,
	})
}

func (app *application) buildRestorePreview(ctx context.Context, payload backupPayload) (restorePreview, error) {
	preview := restorePreview{Valid: true, Errors: make([]string, 0), Conflicts: make([]string, 0)}
	for groupIndex, backupGroup := range payload.Groups {
		groupName := strings.TrimSpace(backupGroup.Name)
		if groupName == "" {
			preview.Errors = append(preview.Errors, fmt.Sprintf("Gruppe %d hat keinen Namen", groupIndex+1))
			continue
		}

		var groupID int64
		err := app.db.QueryRowContext(ctx, `SELECT id FROM groups WHERE lower(name) = lower(?) LIMIT 1`, groupName).Scan(&groupID)
		groupExists := err == nil
		if errors.Is(err, sql.ErrNoRows) {
			preview.NewGroups++
		} else if err != nil {
			return preview, err
		} else {
			preview.ExistingGroups++
			preview.Conflicts = append(preview.Conflicts, fmt.Sprintf("Gruppe: %s", groupName))
		}

		for bookmarkIndex, backupBookmark := range backupGroup.Bookmarks {
			title := strings.TrimSpace(backupBookmark.Title)
			urlValue := strings.TrimSpace(backupBookmark.URL)
			if title == "" {
				preview.Errors = append(preview.Errors, fmt.Sprintf("Lesezeichen %d in %s hat keinen Titel", bookmarkIndex+1, groupName))
			}
			if err := validateURL(urlValue); err != nil {
				preview.Errors = append(preview.Errors, fmt.Sprintf("Lesezeichen %q in %s: %v", title, groupName, err))
			}
			if !groupExists || title == "" || urlValue == "" {
				preview.NewBookmarks++
				continue
			}
			var bookmarkID int64
			err := app.db.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE group_id = ? AND url = ? LIMIT 1`, groupID, urlValue).Scan(&bookmarkID)
			if errors.Is(err, sql.ErrNoRows) {
				preview.NewBookmarks++
			} else if err != nil {
				return preview, err
			} else {
				preview.ConflictingBookmarks++
				preview.Conflicts = append(preview.Conflicts, fmt.Sprintf("Lesezeichen: %s / %s", groupName, title))
			}
		}
	}

	for noteIndex, backupNote := range payload.Notes {
		title := strings.TrimSpace(backupNote.Title)
		if title == "" {
			preview.Errors = append(preview.Errors, fmt.Sprintf("Notiz %d hat keinen Titel", noteIndex+1))
			continue
		}
		var noteID int64
		err := app.db.QueryRowContext(ctx, `SELECT id FROM notes WHERE title = ? LIMIT 1`, title).Scan(&noteID)
		if errors.Is(err, sql.ErrNoRows) {
			preview.NewNotes++
		} else if err != nil {
			return preview, err
		} else {
			preview.ConflictingNotes++
			preview.Conflicts = append(preview.Conflicts, fmt.Sprintf("Notiz: %s", title))
		}
	}
	preview.Valid = len(preview.Errors) == 0
	return preview, nil
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
	if duplicate, err := app.findDuplicateBookmark(r.Context(), urlValue, 0); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	} else if duplicate != "" {
		writeErr(w, http.StatusConflict, fmt.Errorf("doppeltes lesezeichen existiert bereits: %s", duplicate))
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
		if duplicate, err := app.findDuplicateBookmark(r.Context(), urlValue, bookmarkID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else if duplicate != "" {
			writeErr(w, http.StatusConflict, fmt.Errorf("doppeltes lesezeichen existiert bereits: %s", duplicate))
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
	if !app.externalPrices {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("externe preisabfragen sind deaktiviert"))
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
	if !app.externalPrices {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("externe preisabfragen sind deaktiviert"))
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

func (app *application) findDuplicateBookmark(ctx context.Context, rawURL string, excludeID int64) (string, error) {
	targetURL := normalizeBookmarkURL(rawURL)
	rows, err := app.db.QueryContext(ctx, `
		SELECT bookmarks.id, bookmarks.title, bookmarks.url, groups.name
		FROM bookmarks
		JOIN groups ON groups.id = bookmarks.group_id
		WHERE bookmarks.id != ?`, excludeID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var title, candidateURL, groupName string
		if err := rows.Scan(&id, &title, &candidateURL, &groupName); err != nil {
			return "", err
		}
		if normalizeBookmarkURL(candidateURL) == targetURL {
			return fmt.Sprintf("%q in Gruppe %q", title, groupName), nil
		}
	}
	return "", rows.Err()
}

func normalizeBookmarkURL(raw string) string {
	value := strings.TrimSpace(raw)
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return value
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if u.Path != "/" {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func envBool(key string, fallback bool) bool {
	raw, exists := os.LookupEnv(key)
	if !exists {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return parsed
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
	if err := rows.Err(); err != nil {
		log.Printf("removeBookmarkFromNotes: %v", err)
		return
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

func fetchLatestUpdateInfo(ctx context.Context) (updateInfo, error) {
	release, err := fetchLatestGitHubRelease(ctx)
	if err != nil {
		return updateInfo{}, err
	}

	latest := strings.TrimSpace(release.TagName)
	info := updateInfo{
		CurrentVersion: appVersion,
		LatestVersion:  latest,
		ReleaseURL:     release.HTMLURL,
		CanInstall:     runtime.GOOS == "windows",
	}
	if latest == "" {
		return info, fmt.Errorf("github-release enthaelt keine version")
	}

	asset := findReleaseAsset(release, latest)
	if asset.Name != "" {
		info.AssetName = asset.Name
		info.AssetURL = asset.BrowserDownloadURL
	}
	info.UpdateAvailable = isNewerVersion(appVersion, latest)
	if !info.CanInstall {
		info.Message = "installation ist nur unter Windows moeglich"
	} else if info.AssetURL == "" {
		info.CanInstall = false
		info.Message = "im neuesten release wurde keine passende windows-exe gefunden"
	}
	return info, nil
}

func fetchLatestGitHubRelease(ctx context.Context) (githubRelease, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("github konnte nicht erreicht werden: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("github-release-check fehlgeschlagen: status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("github-antwort konnte nicht gelesen werden: %w", err)
	}
	return release, nil
}

func findReleaseAsset(release githubRelease, tag string) githubReleaseAsset {
	expected := fmt.Sprintf("Lesezeichen-Hub_%s.exe", tag)
	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, expected) {
			return asset
		}
	}
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.HasPrefix(name, "lesezeichen-hub_") && strings.HasSuffix(name, ".exe") {
			return asset
		}
	}
	return githubReleaseAsset{}
}

func isNewerVersion(current, latest string) bool {
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" {
		return latest != "dev"
	}

	currentParts, currentOK := numericVersionParts(current)
	latestParts, latestOK := numericVersionParts(latest)
	if currentOK && latestOK {
		maxLen := len(currentParts)
		if len(latestParts) > maxLen {
			maxLen = len(latestParts)
		}
		for i := 0; i < maxLen; i++ {
			var c, l int
			if i < len(currentParts) {
				c = currentParts[i]
			}
			if i < len(latestParts) {
				l = latestParts[i]
			}
			if l > c {
				return true
			}
			if l < c {
				return false
			}
		}
		return false
	}

	return latest != current
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimPrefix(v, "v")
	return v
}

func numericVersionParts(v string) ([]int, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return nil, false
	}
	tokens := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	parts := make([]int, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		value, err := strconv.Atoi(token)
		if err != nil {
			return nil, false
		}
		parts = append(parts, value)
	}
	return parts, len(parts) > 0
}

func downloadFile(ctx context.Context, sourceURL, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download fehlgeschlagen: status %d", resp.StatusCode)
	}

	out, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("update-datei konnte nicht geschrieben werden: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("update-datei konnte nicht gespeichert werden: %w", err)
	}
	return nil
}

func verifyDownloadedAsset(ctx context.Context, assetURL, targetPath string) error {
	checksumURL := assetURL + ".sha256"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("sha256-checksumme nicht erreichbar, ueberspringe pruefung: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		log.Printf("keine sha256-checksumme im release gefunden, ueberspringe pruefung")
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sha256-checksumme konnte nicht geladen werden: status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("sha256-checksumme konnte nicht gelesen werden: %w", err)
	}
	expected := firstSHA256Token(string(raw))
	if expected == "" {
		return fmt.Errorf("sha256-checksumme ist ungueltig")
	}

	file, err := os.Open(targetPath)
	if err != nil {
		return fmt.Errorf("update-datei konnte nicht geprueft werden: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("update-datei konnte nicht geprueft werden: %w", err)
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("sha256-pruefung fehlgeschlagen")
	}
	return nil
}

func firstSHA256Token(raw string) string {
	for _, token := range strings.Fields(raw) {
		token = strings.TrimSpace(token)
		if len(token) != 64 {
			continue
		}
		ok := true
		for _, r := range token {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				ok = false
				break
			}
		}
		if ok {
			return token
		}
	}
	return ""
}

func writeUpdateScript(scriptPath string) error {
	script := `param(
    [Parameter(Mandatory = $true)][string]$TargetExe,
    [Parameter(Mandatory = $true)][string]$NewExe,
    [Parameter(Mandatory = $true)][string]$AppDir,
    [Parameter(Mandatory = $true)][string]$RuntimeDir,
    [Parameter(Mandatory = $true)][string]$Address,
    [Parameter(Mandatory = $true)][int]$OldPid
)

$ErrorActionPreference = 'Stop'

$pidFile = Join-Path $RuntimeDir 'lesezeichen.pid'
$addrFile = Join-Path $RuntimeDir 'lesezeichen.addr'
$logFile = Join-Path $RuntimeDir 'lesezeichen.log'
$errLogFile = Join-Path $RuntimeDir 'lesezeichen.err.log'
$updateLogFile = Join-Path $RuntimeDir 'update.log'

function Write-UpdateLog {
    param([string]$Message)
    "$(Get-Date -Format s) $Message" | Add-Content -Path $updateLogFile -Encoding utf8
}

try {
    Write-UpdateLog "Update startet. Ziel: $TargetExe"
    Start-Sleep -Milliseconds 1500

    $proc = Get-Process -Id $OldPid -ErrorAction SilentlyContinue
    if ($null -ne $proc) {
        $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $OldPid" -ErrorAction SilentlyContinue
        $processPath = if ($null -ne $processInfo) { [string]$processInfo.ExecutablePath } else { '' }
        if ($processPath.Trim() -ieq $TargetExe.Trim()) {
            Stop-Process -Id $OldPid -Force -ErrorAction SilentlyContinue
        } else {
            Write-UpdateLog "PID $OldPid verweist nicht auf $TargetExe. Prozess wird nicht beendet."
        }
    }

    for ($i = 0; $i -lt 80; $i++) {
        $proc = Get-Process -Id $OldPid -ErrorAction SilentlyContinue
        if ($null -eq $proc) { break }
        Start-Sleep -Milliseconds 250
    }

    Copy-Item -LiteralPath $NewExe -Destination $TargetExe -Force
    Remove-Item -LiteralPath $NewExe -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue

    $env:ADDR = $Address
    $newProcess = Start-Process -FilePath $TargetExe -WorkingDirectory $AppDir -WindowStyle Hidden -PassThru -RedirectStandardOutput $logFile -RedirectStandardError $errLogFile
    $newProcess.Id | Set-Content -Path $pidFile -Encoding ascii
    $Address | Set-Content -Path $addrFile -Encoding ascii

    for ($i = 0; $i -lt 50; $i++) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "http://$Address/api/state" -TimeoutSec 1 | Out-Null
            Start-Process "http://$Address" | Out-Null
            Write-UpdateLog "Update abgeschlossen. Neue PID: $($newProcess.Id)"
            exit 0
        } catch {
            if ($newProcess.HasExited) { break }
            Start-Sleep -Milliseconds 200
        }
    }

    Write-UpdateLog "Update installiert, aber Neustart war nicht erreichbar."
    exit 1
} catch {
    Write-UpdateLog "Update fehlgeschlagen: $($_.Exception.Message)"
    exit 1
}
`
	return os.WriteFile(scriptPath, []byte(script), 0644)
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
