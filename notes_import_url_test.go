package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type noteImportRoundTrip func(*http.Request) (*http.Response, error)

func (f noteImportRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNormalizeNoteImportURL(t *testing.T) {
	target, err := normalizeNoteImportURL("https://github.com/example/project/blob/main/docs/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if target.String() != "https://raw.githubusercontent.com/example/project/main/docs/plan.md" {
		t.Fatalf("GitHub-Dateilink wurde nicht normalisiert: %s", target)
	}
	for _, raw := range []string{"file:///tmp/note.txt", "http://127.0.0.1/private", "https://user:secret@example.com/note.txt"} {
		if _, err := normalizeNoteImportURL(raw); err == nil {
			t.Errorf("unsichere URL akzeptiert: %s", raw)
		}
	}
	if _, err := publicNoteDialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Error("private Zieladresse wurde akzeptiert")
	}
}

func TestDownloadNoteTextPreservesContent(t *testing.T) {
	content := "# Plan\n\n\nNächster Schritt\n"
	client := &http.Client{Transport: noteImportRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(content)),
			Request:    r,
		}, nil
	})}
	target, _ := normalizeNoteImportURL("https://raw.githubusercontent.com/example/project/main/plan.md")
	document, status, err := downloadNoteText(context.Background(), client, target)
	if err != nil || status != http.StatusOK {
		t.Fatalf("Download fehlgeschlagen: status=%d err=%v", status, err)
	}
	if document.Title != "plan" || document.Content != content {
		t.Fatalf("Titel oder Zeilenumbrüche geändert: %#v", document)
	}
}

func TestDownloadNoteTextRejectsBinaryAndOversize(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   string
		status int
	}{
		{"binary", "Text\x00Datei", http.StatusUnprocessableEntity},
		{"oversize", strings.Repeat("a", maxImportedNoteBytes+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: noteImportRoundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(test.data)), Request: r}, nil
			})}
			target, _ := normalizeNoteImportURL("https://example.com/config")
			_, status, err := downloadNoteText(context.Background(), client, target)
			if err == nil || status != test.status {
				t.Fatalf("status=%d err=%v", status, err)
			}
		})
	}
}
