package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorMiddlewareLogsServerFailuresWithReference(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusInternalServerError, errors.New("database unavailable"))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/modules", nil))
	var payload apiError
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusInternalServerError || payload.ErrorID == "" || strings.Contains(payload.Error, "database unavailable") {
		t.Fatalf("response = %d %+v", response.Code, payload)
	}
	for _, want := range []string{payload.ErrorID, "status=500", "method=GET", `path="/api/modules"`, "database unavailable"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("log %q missing %q", output.String(), want)
		}
	}

	output.Reset()
	badRequest := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, errors.New("ungueltige eingabe"))
	}))
	badRequest.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/modules", nil))
	if output.Len() != 0 {
		t.Fatalf("client error should not enter error log: %q", output.String())
	}
}

func TestErrorMiddlewareLogsRecoveredPanic(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	handler := recoverMiddleware(loggingMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/test", nil))
	var payload apiError
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusInternalServerError || payload.ErrorID == "" || !strings.Contains(output.String(), payload.ErrorID) || !strings.Contains(output.String(), "test panic") {
		t.Fatalf("response = %d %+v, log = %q", response.Code, payload, output.String())
	}
}

func TestErrorLogRotatesAtSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "error.log")
	writer := &rotatingErrorLog{path: path, maxBytes: 30}
	if _, err := writer.Write([]byte("first error entry\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("second error entry\n")); err != nil {
		t.Fatal(err)
	}
	older, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(older) != "first error entry\n" || string(current) != "second error entry\n" {
		t.Fatalf("rotated = %q, current = %q", older, current)
	}
}
