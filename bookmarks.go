package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

const unsortedGroupName = "Unsortiert"

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
		tx, err := app.db.BeginTx(r.Context(), nil)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer tx.Rollback()

		if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks SET group_id = NULL WHERE group_id = ?`, groupID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}

		res, err := tx.ExecContext(r.Context(), `DELETE FROM groups WHERE id = ?`, groupID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("gruppe nicht gefunden"))
			return
		}
		if err := tx.Commit(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
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
	if duplicate, err := app.findDuplicateBookmark(r.Context(), payload.GroupID, urlValue, 0); err != nil {
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
	if strings.HasSuffix(trimmed, "/open") {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		bookmarkID, err := strconv.ParseInt(strings.TrimSuffix(trimmed, "/open"), 10, 64)
		if err != nil || bookmarkID <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige bookmark-id"))
			return
		}
		var urlValue string
		if err := app.db.QueryRowContext(r.Context(), `SELECT url FROM bookmarks WHERE id = ?`, bookmarkID).Scan(&urlValue); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, fmt.Errorf("bookmark nicht gefunden"))
				return
			}
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if _, err := app.db.ExecContext(r.Context(), `UPDATE bookmarks SET open_count = open_count + 1, last_opened_at = CURRENT_TIMESTAMP WHERE id = ?`, bookmarkID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		http.Redirect(w, r, urlValue, http.StatusFound)
		return
	}
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
		if duplicate, err := app.findDuplicateBookmark(r.Context(), payload.GroupID, urlValue, bookmarkID); err != nil {
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
			nullableGroupID(payload.GroupID),
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
		var err error
		if groupID == 0 {
			_, err = app.db.ExecContext(r.Context(), `UPDATE bookmarks SET sort_order = ? WHERE id = ? AND group_id IS NULL`, i, id)
		} else {
			_, err = app.db.ExecContext(r.Context(), `UPDATE bookmarks SET sort_order = ? WHERE id = ? AND group_id = ?`, i, id, groupID)
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (app *application) listBookmarksByGroup(w http.ResponseWriter, r *http.Request, groupID int64) {
	query := `SELECT id, group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at, open_count, last_opened_at, created_at, updated_at
		 FROM bookmarks WHERE group_id = ? ORDER BY pinned DESC, sort_order ASC, id ASC`
	args := []any{groupID}
	if groupID == 0 {
		query = `SELECT id, group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at, open_count, last_opened_at, created_at, updated_at
		 FROM bookmarks WHERE group_id IS NULL ORDER BY pinned DESC, sort_order ASC, id ASC`
		args = nil
	}
	rows, err := app.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	items := make([]bookmark, 0)
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
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
			`SELECT id, group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at, open_count, last_opened_at, created_at, updated_at
			 FROM bookmarks WHERE group_id = ? ORDER BY pinned DESC, sort_order ASC, id ASC`,
			groups[i].ID,
		)
		if err != nil {
			return nil, err
		}

		items := make([]bookmark, 0)
		for bRows.Next() {
			b, err := scanBookmark(bRows)
			if err != nil {
				bRows.Close()
				return nil, err
			}
			items = append(items, b)
		}
		if err := bRows.Err(); err != nil {
			bRows.Close()
			return nil, err
		}
		bRows.Close()

		groups[i].Bookmarks = items
	}

	unsorted, err := app.fetchUnsortedBookmarks(ctx)
	if err != nil {
		return nil, err
	}
	if len(unsorted) > 0 {
		groups = append(groups, group{
			ID:          0,
			Name:        unsortedGroupName,
			Description: "Lesezeichen ohne Gruppe",
			SortOrder:   len(groups),
			Bookmarks:   unsorted,
		})
	}

	return groups, nil
}

type bookmarkScanner interface {
	Scan(dest ...any) error
}

func scanBookmark(scanner bookmarkScanner) (bookmark, error) {
	var b bookmark
	var groupID sql.NullInt64
	var tagsRaw string
	var favoriteInt int
	var pinnedInt int
	var archivedInt int
	if err := scanner.Scan(&b.ID, &groupID, &b.Title, &b.URL, &b.Notes, &tagsRaw, &favoriteInt, &pinnedInt, &archivedInt, &b.SortOrder, &b.RemindAt, &b.OpenCount, &b.LastOpened, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return b, err
	}
	if groupID.Valid {
		b.GroupID = groupID.Int64
	}
	b.Tags = parseTags(tagsRaw)
	b.Favorite = favoriteInt == 1
	b.Pinned = pinnedInt == 1
	b.Archived = archivedInt == 1
	return b, nil
}

func nullableGroupID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func (app *application) fetchUnsortedBookmarks(ctx context.Context) ([]bookmark, error) {
	rows, err := app.db.QueryContext(
		ctx,
		`SELECT id, group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at, open_count, last_opened_at, created_at, updated_at
		 FROM bookmarks WHERE group_id IS NULL ORDER BY pinned DESC, sort_order ASC, id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]bookmark, 0)
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	return items, rows.Err()
}
