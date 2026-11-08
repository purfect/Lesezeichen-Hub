package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode json: %v", err)
	}
}

type errorResponseWriter struct {
	http.ResponseWriter
	status  int
	errorID string
	cause   error
}

func (writer *errorResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *errorResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *errorResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &errorResponseWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		if writer.status >= http.StatusInternalServerError {
			if writer.errorID == "" {
				writer.errorID = nextErrorID()
			}
			logHTTPFailure(writer.errorID, writer.status, r.Method, r.URL.Path, writer.cause)
		}
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				id := nextErrorID()
				log.Printf("level=ERROR id=%s status=500 method=%s path=%q panic=%q\n%s", id, r.Method, r.URL.Path, rec, debug.Stack())
				writeJSON(w, http.StatusInternalServerError, apiError{Error: publicServerError(http.StatusInternalServerError), ErrorID: id})
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

func readAppSetting(db *sql.DB, key string) string {
	var value string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
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
