package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSearchUsesFullTextIndex(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO bookmarks(title, url, notes, tags) VALUES
			('Quellcode', 'https://github.com/purfect', '', 'dev'),
			('Wetter', 'https://wetter.example', 'Regenradar', 'privat');
		INSERT INTO notes(title, content, type, tags) VALUES
			('Deployment', 'Kubernetes Rollout mit Helm', 'note', ''),
			('Geheim', 'Kubernetes Token', 'vault', '');`); err != nil {
		t.Fatal(err)
	}

	search := func(q string) (ids []int64, notes []struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}) {
		t.Helper()
		response := httptest.NewRecorder()
		app := &application{db: db}
		app.handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?q="+q, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", response.Code, response.Body.String())
		}
		var payload struct {
			BookmarkIDs []int64 `json:"bookmark_ids"`
			Notes       []struct {
				ID      int64  `json:"id"`
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"notes"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload.BookmarkIDs, payload.Notes
	}

	if ids, _ := search("HUB"); len(ids) != 1 {
		t.Fatalf("substring search ids = %v, want one bookmark", ids)
	}
	if ids, _ := search("radar%20pr"); len(ids) != 1 {
		t.Fatalf("combined long/short term ids = %v, want one bookmark", ids)
	}
	_, notes := search("kubernetes")
	if len(notes) != 1 || notes[0].Title != "Deployment" {
		t.Fatalf("notes = %#v, want only the non-vault note", notes)
	}
	if !strings.Contains(notes[0].Snippet, "\u0002") {
		t.Fatalf("snippet %q has no highlight marker", notes[0].Snippet)
	}

	if _, err := db.Exec(`UPDATE notes SET content = 'Nichts mehr' WHERE title = 'Deployment'`); err != nil {
		t.Fatal(err)
	}
	if _, notes := search("kubernetes"); len(notes) != 0 {
		t.Fatalf("notes after update = %#v, want none", notes)
	}
}

func TestAutoBackupWritesAndPrunes(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &application{db: db}
	dir := filepath.Join(testTempDir(t), "backups")

	for _, name := range []string{"lesezeichen-auto-20200101-000000.json", "lesezeichen-auto-20200102-000000.json", "eigene-datei.json"} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := app.createAutoBackup(context.Background(), backupSettings{Enabled: true, Dir: dir, Keep: 2}); err != nil {
		t.Fatal(err)
	}

	files, err := listAutoBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[1].Name != "lesezeichen-auto-20200102-000000.json" {
		t.Fatalf("files = %#v, want newest backup plus 20200102", files)
	}
	if _, err := os.Stat(filepath.Join(dir, "eigene-datei.json")); err != nil {
		t.Fatal("foreign files must not be pruned")
	}

	data, err := os.ReadFile(filepath.Join(dir, files[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	var payload backupPayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.Version != 1 {
		t.Fatalf("backup payload invalid: %v", err)
	}
}

func TestBackupSettingsRejectsNonJSONContentType(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &application{db: db}
	request := httptest.NewRequest(http.MethodPut, "/api/backup-settings", strings.NewReader(`{"enabled":true,"dir":"x","keep":3}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	app.handleBackupSettings(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
	}
}
