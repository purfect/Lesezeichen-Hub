package main

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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
	rows, err := app.db.QueryContext(r.Context(), `SELECT id, name, root_path, source_url, installed_version, managed FROM modules ORDER BY lower(name), id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	modules := make([]localModule, 0)
	for rows.Next() {
		var item localModule
		if err := rows.Scan(&item.ID, &item.Name, &item.Path, &item.SourceURL, &item.InstalledVersion, &item.Managed); err != nil {
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

func (app *application) handleModuleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var payload struct {
		Name      string   `json:"name"`
		SourceURL string   `json:"source_url"`
		Notes     string   `json:"notes"`
		Tags      []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	module, err := app.installExternalModuleArchive(r.Context(), payload.Name, payload.SourceURL, payload.Notes, payload.Tags)
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
	rows, err := app.db.QueryContext(ctx, `SELECT id, name, installed_version, managed FROM modules`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item localModule
		if err := rows.Scan(&item.ID, &item.Name, &item.InstalledVersion, &item.Managed); err != nil {
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
			Category:      catalogModuleCategory(repository.Topics),
			Description:   repository.Description,
			RepositoryURL: repository.HTMLURL,
			DefaultBranch: repository.DefaultBranch,
		}
		module.Version = app.fetchModuleVersion(ctx, repository.Name, repository.DefaultBranch)
		if local, ok := installed[strings.ToLower(repository.Name)]; ok {
			module.Installed = true
			module.LocalID = local.ID
			module.LocalURL = fmt.Sprintf("/modules/%d/index.html", local.ID)
			module.InstalledVersion = local.InstalledVersion
			module.UpdateAvailable = local.Managed && local.InstalledVersion != "" && module.Version != "" && isNewerVersion(local.InstalledVersion, module.Version)
		}
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return strings.ToLower(modules[i].Name) < strings.ToLower(modules[j].Name) })
	return modules, nil
}

func catalogModuleCategory(topics []string) string {
	for _, topic := range topics {
		if strings.EqualFold(strings.TrimSpace(topic), "spiel") {
			return "Spiele"
		}
	}
	for _, topic := range topics {
		if strings.EqualFold(strings.TrimSpace(topic), "werkzeug") {
			return "Werkzeuge"
		}
	}
	return "Sonstiges"
}

func (app *application) fetchModuleVersion(ctx context.Context, repository, branch string) string {
	manifestBase := strings.TrimRight(app.moduleManifestBase, "/")
	if manifestBase == "" {
		return ""
	}
	endpoint := fmt.Sprintf("%s/%s/%s/%s/version.json", manifestBase, moduleGithubOwner, url.PathEscape(repository), url.PathEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var manifest moduleVersionManifest
	if json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.Version)
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
	for index := range repositories {
		topics, defaultBranch := app.fetchModuleDetailsFromWeb(ctx, repositories[index].HTMLURL)
		repositories[index].Topics = topics
		if defaultBranch != "" {
			repositories[index].DefaultBranch = defaultBranch
		}
	}
	return repositories, nil
}

func (app *application) fetchModuleDetailsFromWeb(ctx context.Context, repositoryURL string) ([]string, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repositoryURL, nil)
	if err != nil {
		return nil, ""
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			response.Body.Close()
		}
		return nil, ""
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, ""
	}
	pattern := regexp.MustCompile(`href="/topics/([A-Za-z0-9-]+)"`)
	seen := make(map[string]bool)
	topics := make([]string, 0)
	for _, match := range pattern.FindAllSubmatch(body, -1) {
		topic := strings.ToLower(string(match[1]))
		if !seen[topic] {
			seen[topic] = true
			topics = append(topics, topic)
		}
	}
	branchPattern := regexp.MustCompile(`href="/` + regexp.QuoteMeta(moduleGithubOwner) + `/[A-Za-z0-9_.-]+/commits/([A-Za-z0-9._/-]+?)/?"`)
	branchMatch := branchPattern.FindSubmatch(body)
	if len(branchMatch) < 2 {
		return topics, ""
	}
	return topics, string(branchMatch[1])
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
	result, err := tx.ExecContext(ctx, `INSERT INTO modules(name, root_path, source_url, installed_version, managed) VALUES(?, ?, ?, ?, 1)`, selected.Name, moduleRoot, selected.RepositoryURL, selected.Version)
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

func (app *application) installExternalModuleArchive(ctx context.Context, rawName, rawSourceURL, rawNotes string, tags []string) (catalogModule, error) {
	name := strings.TrimSpace(rawName)
	sourceURL := strings.TrimSpace(rawSourceURL)
	if name == "" || sourceURL == "" {
		return catalogModule{}, fmt.Errorf("name und url sind erforderlich")
	}
	downloadURL, displaySourceURL, err := normalizeRemoteModuleSourceURL(sourceURL)
	if err != nil {
		return catalogModule{}, err
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
	destination := filepath.Join(installBase, safeModuleDirectoryName(name))
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return catalogModule{}, fmt.Errorf("zielordner fuer das modul existiert bereits")
	}

	archive, err := os.CreateTemp(installBase, ".module-import-*.zip")
	if err != nil {
		return catalogModule{}, err
	}
	archivePath := archive.Name()
	archive.Close()
	defer os.Remove(archivePath)
	if err := downloadFile(ctx, downloadURL, archivePath); err != nil {
		return catalogModule{}, err
	}

	staging, err := os.MkdirTemp(installBase, ".module-import-*")
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
	result, err := tx.ExecContext(ctx, `INSERT INTO modules(name, root_path, source_url, installed_version, managed) VALUES(?, ?, ?, 'external', 1)`, name, moduleRoot, displaySourceURL)
	if err != nil {
		return catalogModule{}, fmt.Errorf("modul konnte nicht registriert werden: %w", err)
	}
	moduleID, _ := result.LastInsertId()
	moduleURL := fmt.Sprintf("/modules/%d/index.html", moduleID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, sort_order) SELECT id, ?, ?, ?, ?, 0, 0 FROM groups WHERE name = 'Module'`, name, moduleURL, strings.TrimSpace(rawNotes), serializeTags(tags)); err != nil {
		return catalogModule{}, fmt.Errorf("modulstart konnte nicht angelegt werden: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalogModule{}, err
	}
	removeDestination = false
	return catalogModule{
		Name:          name,
		Description:   strings.TrimSpace(rawNotes),
		RepositoryURL: displaySourceURL,
		Installed:     true,
		LocalID:       moduleID,
		LocalURL:      moduleURL,
	}, nil
}

func (app *application) updateModule(ctx context.Context, moduleID int64) (catalogModule, error) {
	var installedVersion, sourceURL string
	if err := app.db.QueryRowContext(ctx, `SELECT installed_version, source_url FROM modules WHERE id = ?`, moduleID).Scan(&installedVersion, &sourceURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return catalogModule{}, fmt.Errorf("modul nicht gefunden")
		}
		return catalogModule{}, err
	}
	if installedVersion == "external" {
		if strings.TrimSpace(sourceURL) == "" {
			return catalogModule{}, fmt.Errorf("externe quelle ist fuer dieses modul nicht gespeichert")
		}
		return app.updateExternalModuleArchive(ctx, moduleID)
	}
	return app.updateCatalogModule(ctx, moduleID)
}

func (app *application) updateExternalModuleArchive(ctx context.Context, moduleID int64) (catalogModule, error) {
	var moduleName, moduleRoot, sourceURL string
	if err := app.db.QueryRowContext(ctx, `SELECT name, root_path, source_url FROM modules WHERE id = ?`, moduleID).Scan(&moduleName, &moduleRoot, &sourceURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return catalogModule{}, fmt.Errorf("modul nicht gefunden")
		}
		return catalogModule{}, err
	}
	downloadURL, displaySourceURL, err := normalizeRemoteModuleSourceURL(sourceURL)
	if err != nil {
		return catalogModule{}, err
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

	currentTopLevel, err := managedModuleTopLevel(installBase, moduleRoot)
	if err != nil {
		return catalogModule{}, err
	}
	archive, err := os.CreateTemp(installBase, ".module-update-*.zip")
	if err != nil {
		return catalogModule{}, err
	}
	archivePath := archive.Name()
	archive.Close()
	defer os.Remove(archivePath)
	if err := downloadFile(ctx, downloadURL, archivePath); err != nil {
		return catalogModule{}, err
	}

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
	newRoot, err := installModuleArchiveFromFile(archivePath, staging)
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
	if _, err := app.db.ExecContext(ctx, `UPDATE modules SET root_path = ?, source_url = ?, installed_version = 'external' WHERE id = ?`, updatedRoot, displaySourceURL, moduleID); err != nil {
		return catalogModule{}, err
	}
	replaceOK = true

	return catalogModule{
		Name:          moduleName,
		Description:   "Externe Quelle",
		RepositoryURL: displaySourceURL,
		Installed:     true,
		LocalID:       moduleID,
		LocalURL:      fmt.Sprintf("/modules/%d/index.html", moduleID),
	}, nil
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
	if _, err := app.db.ExecContext(ctx, `UPDATE modules SET root_path = ?, installed_version = ? WHERE id = ?`, updatedRoot, selected.Version, moduleID); err != nil {
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

func installModuleArchiveFromFile(archivePath, destination string) (string, error) {
	if err := extractModuleArchive(archivePath, destination); err != nil {
		return "", err
	}
	return findModuleRoot(destination)
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
		module, err := app.updateModule(r.Context(), moduleID)
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
