package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

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
