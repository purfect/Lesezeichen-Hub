package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExportsExcludeInstalledModules(t *testing.T) {
	db := openTestDB(t)
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	moduleResult, err := db.Exec(`INSERT INTO modules(name, root_path) VALUES('Installiertes Modul', ?)`, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	moduleID, _ := moduleResult.LastInsertId()
	groupResult, err := db.Exec(`INSERT INTO groups(name, description) VALUES('Module', 'Installierte Module')`)
	if err != nil {
		t.Fatal(err)
	}
	moduleGroupID, _ := groupResult.LastInsertId()
	groupResult, err = db.Exec(`INSERT INTO groups(name, description) VALUES('Eigene', '')`)
	if err != nil {
		t.Fatal(err)
	}
	normalGroupID, _ := groupResult.LastInsertId()
	moduleURL := fmt.Sprintf("/modules/%d/index.html", moduleID)
	bookmarkResult, err := db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(?, 'Installiertes Modul', ?)`, moduleGroupID, moduleURL)
	if err != nil {
		t.Fatal(err)
	}
	moduleBookmarkID, _ := bookmarkResult.LastInsertId()
	bookmarkResult, err = db.Exec(`INSERT INTO bookmarks(group_id, title, url) VALUES(?, 'Eigener Link', 'https://example.com')`, normalGroupID)
	if err != nil {
		t.Fatal(err)
	}
	normalBookmarkID, _ := bookmarkResult.LastInsertId()
	if _, err := db.Exec(`INSERT INTO notes(title, content, bookmark_ids) VALUES('Meine Notiz', 'Inhalt', ?)`, serializeBookmarkIDs([]int64{moduleBookmarkID, normalBookmarkID})); err != nil {
		t.Fatal(err)
	}
	app := &application{db: db}

	backup, err := app.buildBackupPayload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.Groups) != 1 || len(backup.Groups[0].Bookmarks) != 1 || backup.Groups[0].Bookmarks[0].Title != "Eigener Link" {
		t.Fatalf("backup groups = %+v", backup.Groups)
	}
	if len(backup.Notes) != 1 || len(backup.Notes[0].BookmarkIDs) != 1 || backup.Notes[0].BookmarkIDs[0] != normalBookmarkID {
		t.Fatalf("backup notes = %+v", backup.Notes)
	}

	for _, format := range []string{"json", "csv", "html"} {
		t.Run(format, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.handleExport(response, httptest.NewRequest(http.MethodGet, "/api/export?format="+format, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			if strings.Contains(body, "Installiertes Modul") || strings.Contains(body, moduleURL) || !strings.Contains(body, "Eigener Link") {
				t.Fatalf("unexpected export: %s", body)
			}
		})
	}
}
