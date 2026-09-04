package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(testTempDir(t), "test.db")
	db, err := sql.Open("sqlite", databaseDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testTempDir(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join(".runtime", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, strings.NewReplacer("\\", "_", "/", "_", " ", "_").Replace(t.Name())+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"https://example.com", true},
		{"http://example.com/path", true},
		{"/modules/12/index.html", true},
		{"javascript:alert(1)", false},
		{"file:///C:/secret.txt", false},
		{"example.com", false},
	}

	for _, test := range tests {
		err := validateURL(test.value)
		if (err == nil) != test.valid {
			t.Errorf("validateURL(%q) error = %v, valid = %t", test.value, err, test.valid)
		}
	}
}

func TestSOPSFileFormat(t *testing.T) {
	for _, filename := range []string{"secrets.yaml", "secrets.yml", "secrets.json", "secrets.env", "secrets.ini"} {
		if !sopsFileFormat(filename) {
			t.Errorf("sopsFileFormat(%q) = false, want true", filename)
		}
	}
	for _, filename := range []string{"README.md", "secret.txt", "script.js", ".sops.yaml"} {
		if sopsFileFormat(filename) {
			t.Errorf("sopsFileFormat(%q) = true, want false", filename)
		}
	}
}

func TestSOPSProjectSelectionIsStored(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	root := testTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	app := &application{db: db}
	body := bytes.NewBufferString(`{"path":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`)
	response := httptest.NewRecorder()
	app.handleSOPSProject(response, httptest.NewRequest(http.MethodPut, "/api/sops/project", body))
	if response.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", response.Code, response.Body.String())
	}
	if app.sopsProjectDir == "" || readAppSetting(db, "sops_project_dir") != app.sopsProjectDir {
		t.Fatal("SOPS-Projektordner wurde nicht gespeichert")
	}
	response = httptest.NewRecorder()
	app.handleSOPSProject(response, httptest.NewRequest(http.MethodGet, "/api/sops/project", nil))
	var responsePayload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(response.Body).Decode(&responsePayload); err != nil || responsePayload.Path != app.sopsProjectDir {
		t.Fatalf("GET path = %q, error = %v, want %q", responsePayload.Path, err, app.sopsProjectDir)
	}
}

func TestResolveSOPSFileStaysInProject(t *testing.T) {
	root := testTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(root, "secrets", "production.yaml")
	if err := os.WriteFile(filename, []byte("sops: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveSOPSFile(root, "secrets/production.yaml", true)
	if err != nil || resolved != filename {
		t.Fatalf("resolveSOPSFile = %q, %v; want %q, nil", resolved, err, filename)
	}
	for _, value := range []string{"../outside.yaml", "/outside.yaml", "."} {
		if _, err := resolveSOPSFile(root, value, true); err == nil {
			t.Errorf("resolveSOPSFile(%q) succeeded, want error", value)
		}
	}
	newFile, err := resolveSOPSFile(root, "secrets/new.yaml", false)
	if err != nil || newFile != filepath.Join(root, "secrets", "new.yaml") {
		t.Fatalf("resolve new SOPS file = %q, %v", newFile, err)
	}
}

func TestSOPSFilesListsOnlySupportedProjectFiles(t *testing.T) {
	root := testTempDir(t)
	for filename, content := range map[string]string{"secrets.yaml": "a: 1", "config.json": "{}", ".sops.yaml": "creation_rules: []", "notes.txt": "ignore", ".git/config": "ignore"} {
		fullPath := filepath.Join(root, filepath.FromSlash(filename))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	app := &application{sopsProjectDir: root}
	response := httptest.NewRecorder()
	app.handleSOPSFiles(response, httptest.NewRequest(http.MethodGet, "/api/sops/files", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if cacheControl := response.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cacheControl)
	}
	var payload struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(payload.Files, []string{"config.json", "secrets.yaml"}) {
		t.Fatalf("files = %#v", payload.Files)
	}
}

func TestFirstKMSARN(t *testing.T) {
	content := "sops:\n  kms:\n    - arn: arn:aws:kms:eu-central-1:123456789012:key/example\n"
	if got := firstKMSARN(content); got != "arn:aws:kms:eu-central-1:123456789012:key/example" {
		t.Fatalf("firstKMSARN = %q", got)
	}
}

func TestNormalizeRemoteModuleSourceURL(t *testing.T) {
	tests := []struct {
		value        string
		wantDownload string
		wantSource   string
		valid        bool
	}{
		{
			value:        "https://example.com/app.zip",
			wantDownload: "https://example.com/app.zip",
			wantSource:   "https://example.com/app.zip",
			valid:        true,
		},
		{
			value:        "https://github.com/acme/tools",
			wantDownload: "https://api.github.com/repos/acme/tools/zipball",
			wantSource:   "https://github.com/acme/tools",
			valid:        true,
		},
		{
			value:        "https://github.com/acme/tools/tree/develop",
			wantDownload: "https://api.github.com/repos/acme/tools/zipball/develop",
			wantSource:   "https://github.com/acme/tools",
			valid:        true,
		},
		{value: "https://example.com/app", valid: false},
	}

	for _, test := range tests {
		downloadURL, sourceURL, err := normalizeRemoteModuleSourceURL(test.value)
		if (err == nil) != test.valid {
			t.Fatalf("normalizeRemoteModuleSourceURL(%q) error = %v, valid = %t", test.value, err, test.valid)
		}
		if err != nil {
			continue
		}
		if downloadURL != test.wantDownload || sourceURL != test.wantSource {
			t.Fatalf("normalizeRemoteModuleSourceURL(%q) = (%q, %q), want (%q, %q)", test.value, downloadURL, sourceURL, test.wantDownload, test.wantSource)
		}
	}
}

func TestNormalizeBookmarkURL(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"HTTPS://Example.COM:443/path/#section", "https://example.com/path#section"},
		{"http://example.com:80", "http://example.com/"},
		{"https://example.com/path/", "https://example.com/path"},
		{"/modules/12/index.html", "/modules/12/index.html"},
	}

	for _, test := range tests {
		if got := normalizeBookmarkURL(test.value); got != test.want {
			t.Errorf("normalizeBookmarkURL(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestFindDuplicateBookmarkRecognizesNormalizedURL(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	result, err := db.Exec(`INSERT INTO groups(name) VALUES('Arbeit')`)
	if err != nil {
		t.Fatal(err)
	}
	groupID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(?, ?, ?)`, groupID, "Runbook", "https://example.com/path/#section")
	if err != nil {
		t.Fatal(err)
	}
	bookmarkID, _ := result.LastInsertId()

	app := &application{db: db}
	duplicate, err := app.findDuplicateBookmark(context.Background(), "HTTPS://EXAMPLE.COM:443/path/#section", 0)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate == "" {
		t.Fatal("normalisierte URL wurde nicht als Duplikat erkannt")
	}

	distinctURLs := []string{
		"https://example.com/other-path#section",
		"https://example.com/path?ticket=2#section",
		"https://example.com/path#other-section",
	}
	for _, distinctURL := range distinctURLs {
		duplicate, err = app.findDuplicateBookmark(context.Background(), distinctURL, 0)
		if err != nil {
			t.Fatal(err)
		}
		if duplicate != "" {
			t.Errorf("eigenstaendige URL derselben Website wurde als Duplikat erkannt: %s", distinctURL)
		}
	}

	duplicate, err = app.findDuplicateBookmark(context.Background(), "https://example.com/path#section", bookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate != "" {
		t.Errorf("bearbeitetes Lesezeichen wurde als eigenes Duplikat erkannt: %s", duplicate)
	}
}

func TestModuleDirectoryServesIndexFile(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	moduleDir := testTempDir(t)
	if err := os.WriteFile(filepath.Join(moduleDir, "index.html"), []byte("<h1>Modul</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO modules(name, root_path) VALUES(?, ?)`, "Testmodul", moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	moduleID, _ := result.LastInsertId()

	app := &application{db: db}
	request := httptest.NewRequest(http.MethodGet, "/modules/"+strconv.FormatInt(moduleID, 10)+"/", nil)
	response := httptest.NewRecorder()
	app.handleModuleFiles(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "<h1>Modul</h1>" {
		t.Errorf("body = %q, want module index", response.Body.String())
	}
}

func TestModuleIndexURLIsServedWithoutRedirect(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	moduleDir := testTempDir(t)
	indexPath := filepath.Join(moduleDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<h1>Version 1</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO modules(name, root_path) VALUES(?, ?)`, "Testmodul", moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	moduleID, _ := result.LastInsertId()

	app := &application{db: db}
	request := httptest.NewRequest(http.MethodGet, "/modules/"+strconv.FormatInt(moduleID, 10)+"/index.html", nil)
	response := httptest.NewRecorder()
	app.handleModuleFiles(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (kein Redirect auf .../)", response.Code, http.StatusOK)
	}
	if location := response.Header().Get("Location"); location != "" {
		t.Errorf("Location = %q, es darf keine Weiterleitung gesendet werden", location)
	}
	if cacheControl := response.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cacheControl)
	}
	if response.Body.String() != "<h1>Version 1</h1>" {
		t.Errorf("body = %q, want erste Version", response.Body.String())
	}
}

func TestModuleFileChangesAreServedImmediately(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	moduleDir := testTempDir(t)
	indexPath := filepath.Join(moduleDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<h1>Version 1</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO modules(name, root_path) VALUES(?, ?)`, "Testmodul", moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	moduleID, _ := result.LastInsertId()

	app := &application{db: db}
	first := httptest.NewRecorder()
	app.handleModuleFiles(first, httptest.NewRequest(http.MethodGet, "/modules/"+strconv.FormatInt(moduleID, 10)+"/index.html", nil))
	if first.Body.String() != "<h1>Version 1</h1>" {
		t.Fatalf("body = %q, want erste Version", first.Body.String())
	}

	if err := os.WriteFile(indexPath, []byte("<h1>Version 2</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := httptest.NewRecorder()
	app.handleModuleFiles(second, httptest.NewRequest(http.MethodGet, "/modules/"+strconv.FormatInt(moduleID, 10)+"/index.html", nil))
	if second.Body.String() != "<h1>Version 2</h1>" {
		t.Errorf("body = %q, geaenderte Datei wurde nicht ausgeliefert", second.Body.String())
	}
}

func TestModuleFilesRejectSymlinkOutsideRoot(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	moduleDir := testTempDir(t)
	if err := os.WriteFile(filepath.Join(moduleDir, "index.html"), []byte("<h1>Modul</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := testTempDir(t)
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("nicht ausliefern"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(moduleDir, "secret.txt")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Skipf("Symlink kann auf diesem System nicht angelegt werden: %v", err)
	}
	result, err := db.Exec(`INSERT INTO modules(name, root_path) VALUES(?, ?)`, "Testmodul", moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	moduleID, _ := result.LastInsertId()

	app := &application{db: db}
	response := httptest.NewRecorder()
	app.handleModuleFiles(response, httptest.NewRequest(http.MethodGet, "/modules/"+strconv.FormatInt(moduleID, 10)+"/secret.txt", nil))

	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestModuleCanBeUpdatedAndDeletedCompletely(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	moduleDir := testTempDir(t)
	if err := os.WriteFile(filepath.Join(moduleDir, "index.html"), []byte("<h1>Modul</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	groupResult, _ := db.Exec(`INSERT INTO groups(name) VALUES('Module')`)
	groupID, _ := groupResult.LastInsertId()
	moduleResult, _ := db.Exec(`INSERT INTO modules(name, root_path) VALUES('Alt', ?)`, moduleDir)
	moduleID, _ := moduleResult.LastInsertId()
	moduleURL := "/modules/" + strconv.FormatInt(moduleID, 10) + "/index.html"
	if _, err := db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(?, 'Alt', ?)`, groupID, moduleURL); err != nil {
		t.Fatal(err)
	}

	app := &application{db: db}
	updateBody, err := json.Marshal(map[string]string{"name": "Neu", "path": moduleDir})
	if err != nil {
		t.Fatal(err)
	}
	updateResponse := httptest.NewRecorder()
	app.handleModuleRoutes(updateResponse, httptest.NewRequest(http.MethodPut, "/api/modules/"+strconv.FormatInt(moduleID, 10), bytes.NewReader(updateBody)))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var bookmarkTitle string
	if err := db.QueryRow(`SELECT title FROM bookmarks WHERE url = ?`, moduleURL).Scan(&bookmarkTitle); err != nil || bookmarkTitle != "Neu" {
		t.Fatalf("bookmark title = %q, error = %v", bookmarkTitle, err)
	}

	deleteResponse := httptest.NewRecorder()
	app.handleModuleRoutes(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/modules/"+strconv.FormatInt(moduleID, 10), nil))
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var modules, bookmarks int
	_ = db.QueryRow(`SELECT COUNT(*) FROM modules`).Scan(&modules)
	_ = db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE url = ?`, moduleURL).Scan(&bookmarks)
	if modules != 0 || bookmarks != 0 {
		t.Errorf("modules = %d, bookmarks = %d, want 0/0", modules, bookmarks)
	}
}

func TestCreatedModuleBookmarkIsNotFavoriteByDefault(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	groupResult, err := db.Exec(`INSERT INTO groups(name) VALUES('Module')`)
	if err != nil {
		t.Fatal(err)
	}
	groupID, _ := groupResult.LastInsertId()

	moduleDir := testTempDir(t)
	if err := os.WriteFile(filepath.Join(moduleDir, "index.html"), []byte("<h1>Modul</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"group_id": groupID,
		"name":     "Werkplan",
		"path":     moduleDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	app := &application{db: db}
	response := httptest.NewRecorder()
	app.handleModules(response, httptest.NewRequest(http.MethodPost, "/api/modules", bytes.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	var favorite int
	if err := db.QueryRow(`SELECT favorite FROM bookmarks WHERE title = 'Werkplan'`).Scan(&favorite); err != nil {
		t.Fatal(err)
	}
	if favorite != 0 {
		t.Fatalf("favorite = %d, want 0", favorite)
	}
}

func TestCatalogModuleCanBeInstalledAndReportedAsInstalled(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	indexFile, err := zipWriter.Create("example-main/public/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexFile.Write([]byte("<h1>Katalogmodul</h1>")); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/orgs/Lesezeichen-Hub/repos":
			writeJSON(w, http.StatusOK, []githubRepository{
				{
					Name:          "Beispiel-Modul",
					Description:   "Ein Testmodul",
					HTMLURL:       "https://github.com/Lesezeichen-Hub/Beispiel-Modul",
					DefaultBranch: "main",
				},
				{
					Name:          ".github",
					Description:   "Organisationsprofil",
					HTMLURL:       "https://github.com/Lesezeichen-Hub/.github",
					DefaultBranch: "main",
				},
			})
		case r.URL.Path == "/repos/Lesezeichen-Hub/Beispiel-Modul/zipball/main":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive.Bytes())
		case r.URL.Path == "/Lesezeichen-Hub/Beispiel-Modul/main/version.json":
			writeJSON(w, http.StatusOK, moduleVersionManifest{Version: "1.0.0"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer github.Close()

	app := &application{
		db:                 db,
		moduleAPIBase:      github.URL,
		moduleManifestBase: github.URL,
		moduleInstallDir:   testTempDir(t),
	}
	installResponse := httptest.NewRecorder()
	app.handleModuleCatalogRoutes(installResponse, httptest.NewRequest(http.MethodPost, "/api/module-catalog/Beispiel-Modul/install", nil))
	if installResponse.Code != http.StatusCreated {
		t.Fatalf("install status = %d, body = %s", installResponse.Code, installResponse.Body.String())
	}

	var moduleID int64
	var rootPath string
	if err := db.QueryRow(`SELECT id, root_path FROM modules WHERE name = 'Beispiel-Modul'`).Scan(&moduleID, &rootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "index.html")); err != nil {
		t.Fatalf("installierte index.html fehlt: %v", err)
	}
	var installedVersion string
	if err := db.QueryRow(`SELECT installed_version FROM modules WHERE id = ?`, moduleID).Scan(&installedVersion); err != nil || installedVersion != "1.0.0" {
		t.Fatalf("installierte Version = %q, error = %v, want 1.0.0", installedVersion, err)
	}
	var bookmarkCount int
	moduleURL := "/modules/" + strconv.FormatInt(moduleID, 10) + "/index.html"
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE title = 'Beispiel-Modul' AND url = ?`, moduleURL).Scan(&bookmarkCount); err != nil || bookmarkCount != 1 {
		t.Fatalf("bookmark count = %d, error = %v", bookmarkCount, err)
	}
	var favorite int
	if err := db.QueryRow(`SELECT favorite FROM bookmarks WHERE title = 'Beispiel-Modul' AND url = ?`, moduleURL).Scan(&favorite); err != nil {
		t.Fatalf("favorite konnte nicht gelesen werden: %v", err)
	}
	if favorite != 0 {
		t.Fatalf("favorite = %d, want 0", favorite)
	}

	catalogResponse := httptest.NewRecorder()
	app.handleModuleCatalog(catalogResponse, httptest.NewRequest(http.MethodGet, "/api/module-catalog", nil))
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, body = %s", catalogResponse.Code, catalogResponse.Body.String())
	}
	var payload struct {
		Modules []catalogModule `json:"modules"`
	}
	if err := json.Unmarshal(catalogResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Modules) != 1 || !payload.Modules[0].Installed || payload.Modules[0].LocalURL != moduleURL {
		t.Fatalf("catalog modules = %+v", payload.Modules)
	}

	deleteResponse := httptest.NewRecorder()
	app.handleModuleRoutes(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/modules/"+strconv.FormatInt(moduleID, 10), nil))
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(rootPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verwalteter Modulordner wurde nicht entfernt: %v", err)
	}
}

func TestExternalModuleArchiveCanBeInstalled(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	indexFile, err := zipWriter.Create("webapp-main/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexFile.Write([]byte("<h1>Externe Webapp</h1>")); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webapp.zip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive.Bytes())
	}))
	defer source.Close()

	app := &application{db: db, moduleInstallDir: testTempDir(t)}
	body, err := json.Marshal(map[string]any{
		"name":       "Externe Webapp",
		"source_url": source.URL + "/webapp.zip",
		"notes":      "Aus Testquelle",
		"tags":       []string{"tools", "extern"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.handleModuleImport(response, httptest.NewRequest(http.MethodPost, "/api/module-import", bytes.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("install status = %d, body = %s", response.Code, response.Body.String())
	}

	var moduleID int64
	var moduleRoot string
	var sourceURL string
	var installedVersion string
	var managed int
	if err := db.QueryRow(`SELECT id, root_path, source_url, installed_version, managed FROM modules WHERE name = 'Externe Webapp'`).Scan(&moduleID, &moduleRoot, &sourceURL, &installedVersion, &managed); err != nil {
		t.Fatal(err)
	}
	if managed != 1 {
		t.Fatalf("managed = %d, want 1", managed)
	}
	if installedVersion != "external" {
		t.Fatalf("installed version = %q, want external", installedVersion)
	}
	if sourceURL != source.URL+"/webapp.zip" {
		t.Fatalf("source url = %q, want %q", sourceURL, source.URL+"/webapp.zip")
	}
	if _, err := os.Stat(filepath.Join(moduleRoot, "index.html")); err != nil {
		t.Fatalf("index.html wurde nicht installiert: %v", err)
	}
	var bookmarkURL, tags string
	if err := db.QueryRow(`SELECT url, tags FROM bookmarks WHERE title = 'Externe Webapp'`).Scan(&bookmarkURL, &tags); err != nil {
		t.Fatal(err)
	}
	wantURL := "/modules/" + strconv.FormatInt(moduleID, 10) + "/index.html"
	if bookmarkURL != wantURL {
		t.Fatalf("bookmark url = %q, want %q", bookmarkURL, wantURL)
	}
	if tags != "tools,extern" {
		t.Fatalf("tags = %q, want tools,extern", tags)
	}
}

func TestExternalModuleArchiveCanBeReloaded(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	makeArchive := func(indexBody string) []byte {
		var archive bytes.Buffer
		zipWriter := zip.NewWriter(&archive)
		indexFile, err := zipWriter.Create("webapp-main/index.html")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := indexFile.Write([]byte(indexBody)); err != nil {
			t.Fatal(err)
		}
		if err := zipWriter.Close(); err != nil {
			t.Fatal(err)
		}
		return archive.Bytes()
	}
	firstArchive := makeArchive("<h1>Version 1</h1>")
	secondArchive := makeArchive("<h1>Version 2</h1>")
	archiveRequests := 0

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webapp.zip" {
			http.NotFound(w, r)
			return
		}
		archiveRequests++
		w.Header().Set("Content-Type", "application/zip")
		if archiveRequests == 1 {
			_, _ = w.Write(firstArchive)
			return
		}
		_, _ = w.Write(secondArchive)
	}))
	defer source.Close()

	app := &application{db: db, moduleInstallDir: testTempDir(t)}
	body, err := json.Marshal(map[string]any{
		"name":       "Externe Webapp",
		"source_url": source.URL + "/webapp.zip",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.handleModuleImport(response, httptest.NewRequest(http.MethodPost, "/api/module-import", bytes.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("install status = %d, body = %s", response.Code, response.Body.String())
	}

	var moduleID int64
	var moduleRoot string
	if err := db.QueryRow(`SELECT id, root_path FROM modules WHERE name = 'Externe Webapp'`).Scan(&moduleID, &moduleRoot); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(moduleRoot, "index.html")); err != nil || string(body) != "<h1>Version 1</h1>" {
		t.Fatalf("installierte index.html = %q, error = %v", string(body), err)
	}

	updateResponse := httptest.NewRecorder()
	app.handleModuleRoutes(updateResponse, httptest.NewRequest(http.MethodPost, "/api/modules/"+strconv.FormatInt(moduleID, 10)+"/update", nil))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	if err := db.QueryRow(`SELECT root_path FROM modules WHERE id = ?`, moduleID).Scan(&moduleRoot); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(moduleRoot, "index.html")); err != nil || string(body) != "<h1>Version 2</h1>" {
		t.Fatalf("aktualisierte index.html = %q, error = %v", string(body), err)
	}
}

func TestCatalogModuleCanBeUpdated(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	makeArchive := func(indexBody string, includeOldFile bool) []byte {
		var archive bytes.Buffer
		zipWriter := zip.NewWriter(&archive)
		indexFile, err := zipWriter.Create("example-main/public/index.html")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := indexFile.Write([]byte(indexBody)); err != nil {
			t.Fatal(err)
		}
		if includeOldFile {
			oldFile, err := zipWriter.Create("example-main/public/old.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := oldFile.Write([]byte("old")); err != nil {
				t.Fatal(err)
			}
		}
		if err := zipWriter.Close(); err != nil {
			t.Fatal(err)
		}
		return archive.Bytes()
	}
	firstArchive := makeArchive("<h1>Version 1</h1>", true)
	secondArchive := makeArchive("<h1>Version 2</h1>", false)
	archiveRequests := 0

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/orgs/Lesezeichen-Hub/repos":
			writeJSON(w, http.StatusOK, []githubRepository{{
				Name:          "Beispiel-Modul",
				Description:   "Ein Testmodul",
				HTMLURL:       "https://github.com/Lesezeichen-Hub/Beispiel-Modul",
				DefaultBranch: "main",
			}})
		case r.URL.Path == "/repos/Lesezeichen-Hub/Beispiel-Modul/zipball/main":
			archiveRequests++
			w.Header().Set("Content-Type", "application/zip")
			if archiveRequests == 1 {
				_, _ = w.Write(firstArchive)
				return
			}
			_, _ = w.Write(secondArchive)
		case r.URL.Path == "/Lesezeichen-Hub/Beispiel-Modul/main/version.json":
			version := "1.0.0"
			if archiveRequests > 0 {
				version = "1.1.0"
			}
			writeJSON(w, http.StatusOK, moduleVersionManifest{Version: version})
		default:
			http.NotFound(w, r)
		}
	}))
	defer github.Close()

	app := &application{
		db:                 db,
		moduleAPIBase:      github.URL,
		moduleManifestBase: github.URL,
		moduleInstallDir:   testTempDir(t),
	}
	installResponse := httptest.NewRecorder()
	app.handleModuleCatalogRoutes(installResponse, httptest.NewRequest(http.MethodPost, "/api/module-catalog/Beispiel-Modul/install", nil))
	if installResponse.Code != http.StatusCreated {
		t.Fatalf("install status = %d, body = %s", installResponse.Code, installResponse.Body.String())
	}

	var moduleID int64
	var rootPath string
	if err := db.QueryRow(`SELECT id, root_path FROM modules WHERE name = 'Beispiel-Modul'`).Scan(&moduleID, &rootPath); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(rootPath, "index.html")); err != nil || string(body) != "<h1>Version 1</h1>" {
		t.Fatalf("installed index = %q, error = %v", body, err)
	}

	updateResponse := httptest.NewRecorder()
	app.handleModuleRoutes(updateResponse, httptest.NewRequest(http.MethodPost, "/api/modules/"+strconv.FormatInt(moduleID, 10)+"/update", nil))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	if err := db.QueryRow(`SELECT root_path FROM modules WHERE id = ?`, moduleID).Scan(&rootPath); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(rootPath, "index.html")); err != nil || string(body) != "<h1>Version 2</h1>" {
		t.Fatalf("updated index = %q, error = %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alte Moduldatei existiert nach Update noch: %v", err)
	}
	var installedVersion string
	if err := db.QueryRow(`SELECT installed_version FROM modules WHERE id = ?`, moduleID).Scan(&installedVersion); err != nil || installedVersion != "1.1.0" {
		t.Fatalf("aktualisierte Version = %q, error = %v, want 1.1.0", installedVersion, err)
	}
}

func TestModuleCatalogFallsBackWhenGithubRateLimitIsExhausted(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO modules(name, root_path) VALUES('Gorilla', 'C:\missing')`); err != nil {
		t.Fatal(err)
	}

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/orgs/Lesezeichen-Hub/repos":
			w.Header().Set("X-RateLimit-Remaining", "0")
			writeErr(w, http.StatusForbidden, errors.New("API rate limit exceeded"))
		case r.URL.Path == "/orgs/Lesezeichen-Hub/repositories":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<a href="/Lesezeichen-Hub/.github">.github</a><a href="/Lesezeichen-Hub/Gorilla">Gorilla</a><a href="/Lesezeichen-Hub/NAT_Rechner">NAT Rechner</a>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer github.Close()

	app := &application{db: db, moduleAPIBase: github.URL, moduleWebBase: github.URL}
	modules, err := app.fetchModuleCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 {
		t.Fatalf("modules = %+v, want 2 entries", modules)
	}
	if !modules[0].Installed || modules[0].Name != "Gorilla" {
		t.Fatalf("installed module = %+v", modules[0])
	}
	if modules[1].Name != "NAT_Rechner" || modules[1].DefaultBranch != "main" {
		t.Fatalf("fallback module = %+v", modules[1])
	}
}

func TestModuleCatalogReadsOptionalVersionManifest(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO modules(name, root_path, installed_version, managed) VALUES(?, ?, ?, 1)`, "ansible-vault-encryption", "C:\\modules\\vault", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orgs/Lesezeichen-Hub/repos":
			writeJSON(w, http.StatusOK, []githubRepository{{Name: "ansible-vault-encryption", DefaultBranch: "main"}})
		case "/Lesezeichen-Hub/ansible-vault-encryption/main/version.json":
			writeJSON(w, http.StatusOK, moduleVersionManifest{Version: "1.1.0"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := &application{db: db, moduleAPIBase: server.URL, moduleManifestBase: server.URL}
	modules, err := app.fetchModuleCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 1 || modules[0].Version != "1.1.0" || modules[0].InstalledVersion != "1.0.0" || !modules[0].UpdateAvailable {
		t.Fatalf("modules = %+v, want available update from manifest", modules)
	}
}

func TestInitializeSchemaEnablesBookmarkStorage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := initializeSchema(db); err != nil {
		t.Fatalf("initializeSchema() error = %v", err)
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
	if _, err := db.Exec(`INSERT INTO groups(name) VALUES('Test')`); err != nil {
		t.Fatalf("schema is not writable: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database not created: %v", err)
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("ENABLE_EXTERNAL_PRICES", "false")
	if envBool("ENABLE_EXTERNAL_PRICES", true) {
		t.Error("envBool ignored explicit false")
	}
	t.Setenv("ENABLE_EXTERNAL_PRICES", "invalid")
	if !envBool("ENABLE_EXTERNAL_PRICES", true) {
		t.Error("envBool did not use fallback for invalid input")
	}
}

func TestMetalPricesAreUnavailableWhenDisabled(t *testing.T) {
	app := &application{externalPrices: false}
	request := httptest.NewRequest(http.MethodGet, "/api/metal-prices", nil)
	response := httptest.NewRecorder()

	app.handleMetalPrices(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"2.6", "2.7", true},
		{"2.7", "2.7", false},
		{"2.10", "2.9", false},
		{"v2.6.1", "2.7.0", true},
		{"dev", "2.7", true},
		{"2.7", "", false},
	}

	for _, test := range tests {
		if got := isNewerVersion(test.current, test.latest); got != test.want {
			t.Errorf("isNewerVersion(%q, %q) = %t, want %t", test.current, test.latest, got, test.want)
		}
	}
}

func TestFindReleaseAssetPrefersExactVersionedExe(t *testing.T) {
	release := githubRelease{
		TagName: "2.7",
		Assets: []githubReleaseAsset{
			{Name: "Lesezeichen-Hub_2.6.exe", BrowserDownloadURL: "old"},
			{Name: "Lesezeichen-Hub_2.7.exe", BrowserDownloadURL: "new"},
		},
	}

	asset := findReleaseAsset(release, "2.7")
	if asset.BrowserDownloadURL != "new" {
		t.Errorf("findReleaseAsset() = %q, want exact release asset", asset.BrowserDownloadURL)
	}
}

func TestFetchGitHubReleaseExplainsRateLimit(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer github.Close()

	_, err := fetchGitHubRelease(context.Background(), github.URL)
	if err == nil || !strings.Contains(err.Error(), "github-api-limit erreicht") {
		t.Fatalf("error = %v, want rate-limit explanation", err)
	}
}

func TestFetchUpdateInfoFromManifestAvoidsGitHubAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version.json" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, updateManifest{
			LatestVersion: "3.3",
			AssetName:     "Lesezeichen-Hub_3.3.exe",
			AssetURL:      "https://example.test/Lesezeichen-Hub_3.3.exe",
			ChecksumURL:   "https://example.test/Lesezeichen-Hub_3.3.exe.sha256",
			ReleaseURL:    "https://example.test/releases/3.3",
		})
	}))
	defer server.Close()

	info, err := fetchUpdateInfoFromManifest(context.Background(), server.URL+"/version.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestVersion != "3.3" || info.AssetName != "Lesezeichen-Hub_3.3.exe" || info.ChecksumURL == "" {
		t.Fatalf("update info = %+v, want manifest values", info)
	}
}

func TestDeletingGroupCascadesToItsBookmarks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")
	db, err := sql.Open("sqlite", databaseDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}

	result, err := db.Exec(`INSERT INTO groups(name) VALUES('Module')`)
	if err != nil {
		t.Fatal(err)
	}
	groupID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(?, ?, ?)`, groupID, "Werkplan", "/modules/1/index.html"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`DELETE FROM groups WHERE id = ?`, groupID); err != nil {
		t.Fatal(err)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE url = '/modules/1/index.html'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("verbliebene Lesezeichen = %d, want 0 (Gruppenloeschung muss kaskadieren)", remaining)
	}
}

func TestInitializeSchemaRemovesOrphanedBookmarks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")
	db, err := sql.Open("sqlite", databaseDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	// Waise erzeugen, wie sie ohne aktive Fremdschluessel entstanden ist.
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(9999, 'Werkplan', '/modules/1/index.html')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}

	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE url = '/modules/1/index.html'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("verwaiste Lesezeichen = %d, want 0", remaining)
	}
}

func TestRestorePreviewDoesNotWriteData(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"version":1,"groups":[{"name":"Arbeit","bookmarks":[{"title":"Go","url":"https://go.dev"}]}],"notes":[]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/restore?preview=1", bytes.NewReader(body))
	response := httptest.NewRecorder()
	app := &application{db: db}
	app.handleRestore(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var groupCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM groups`).Scan(&groupCount); err != nil {
		t.Fatal(err)
	}
	if groupCount != 0 {
		t.Errorf("gruppen nach Vorschau = %d, want 0", groupCount)
	}
}

func TestRestoreRejectsUnsafeURL(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"version":1,"groups":[{"name":"Arbeit","bookmarks":[{"title":"Unsicher","url":"javascript:alert(1)"}]}],"notes":[]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/restore", bytes.NewReader(body))
	response := httptest.NewRecorder()
	app := &application{db: db}
	app.handleRestore(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
