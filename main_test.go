package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

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

func TestNormalizeBookmarkURL(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"HTTPS://Example.COM:443/path/#section", "https://example.com/path"},
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
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}

	result, err := db.Exec(`INSERT INTO groups(name) VALUES('Arbeit')`)
	if err != nil {
		t.Fatal(err)
	}
	groupID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(?, ?, ?)`, groupID, "Runbook", "https://example.com/path/")
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

	duplicate, err = app.findDuplicateBookmark(context.Background(), "https://example.com/path", bookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate != "" {
		t.Errorf("bearbeitetes Lesezeichen wurde als eigenes Duplikat erkannt: %s", duplicate)
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
