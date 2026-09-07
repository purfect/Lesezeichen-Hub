package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (app *application) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" && format != "html" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges format; erlaubt: json, csv, html"))
		return
	}

	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltige group_id"))
		return
	}

	groups, err := app.fetchState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if groupID > 0 {
		groups = filterGroupsByID(groups, groupID)
		if len(groups) == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("gruppe nicht gefunden"))
			return
		}
	}

	stamp := time.Now().Format("20060102-150405")
	namePart := "lesezeichen"
	if groupID > 0 {
		namePart = fmt.Sprintf("gruppe-%d", groupID)
	}

	switch format {
	case "json":
		filename := fmt.Sprintf("%s-export-%s.json", namePart, stamp)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := json.NewEncoder(w).Encode(map[string]any{"groups": groups}); err != nil {
			log.Printf("encode export json: %v", err)
		}
	case "csv":
		filename := fmt.Sprintf("%s-export-%s.csv", namePart, stamp)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := writeCSVExport(w, groups); err != nil {
			log.Printf("encode export csv: %v", err)
		}
	case "html":
		filename := fmt.Sprintf("%s-startseite-%s.html", namePart, stamp)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := writeHTMLExport(w, groups); err != nil {
			log.Printf("encode export html: %v", err)
		}
	}
}

func (app *application) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	limitedBody := io.LimitReader(r.Body, 10*1024*1024)
	defer r.Body.Close()
	bodyBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("request body konnte nicht gelesen werden"))
		return
	}

	var payload importPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}

	var speedDial speedDialImport
	isSpeedDialImport := false
	if err := json.Unmarshal(bodyBytes, &speedDial); err == nil && len(speedDial.Dials) > 0 {
		isSpeedDialImport = true
		bookmarks := make([]importBookmark, 0, len(speedDial.Dials))
		for i, dial := range speedDial.Dials {
			bookmarks = append(bookmarks, importBookmark{
				Title:     dial.Title,
				URL:       dial.URL,
				Archived:  false,
				SortOrder: i,
			})
		}

		payload.Groups = []importGroup{
			{
				Name:      "ungruppiert",
				SortOrder: 0,
				Bookmarks: bookmarks,
			},
		}
	}

	if payload.Groups == nil {
		payload.Groups = []importGroup{}
	}

	tx, err := app.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	seenNames := make(map[string]struct{})
	createdGroups := 0
	createdBookmarks := 0
	updatedBookmarks := 0

	for _, g := range payload.Groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("gruppenname ist erforderlich"))
			return
		}

		key := strings.ToLower(name)
		if _, exists := seenNames[key]; exists {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("doppelte gruppe im import: %s", name))
			return
		}
		seenNames[key] = struct{}{}

		var groupID int64
		description := strings.TrimSpace(g.Description)
		err := tx.QueryRowContext(
			r.Context(),
			`SELECT id FROM groups WHERE lower(name) = lower(?) LIMIT 1`,
			name,
		).Scan(&groupID)
		if errors.Is(err, sql.ErrNoRows) {
			result, err := tx.ExecContext(
				r.Context(),
				`INSERT INTO groups(name, description, sort_order) VALUES(?, ?, ?)`,
				name,
				description,
				g.SortOrder,
			)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			groupID, _ = result.LastInsertId()
			createdGroups++
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else {
			if !isSpeedDialImport {
				if _, err := tx.ExecContext(
					r.Context(),
					`UPDATE groups SET description = ?, sort_order = ? WHERE id = ?`,
					description,
					g.SortOrder,
					groupID,
				); err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
			}
		}

		for _, b := range g.Bookmarks {
			title := strings.TrimSpace(b.Title)
			if title == "" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("bookmark-titel ist erforderlich"))
				return
			}

			urlValue := strings.TrimSpace(b.URL)
			if err := validateURL(urlValue); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}

			notes := strings.TrimSpace(b.Notes)
			remindAt, err := parseOptionalDateTime(b.RemindAt)
			if err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges remind_at bei %q", title))
				return
			}
			tagsRaw := serializeTags(b.Tags)
			favoriteInt := boolToInt(b.Favorite)
			pinnedInt := boolToInt(b.Pinned)
			archivedInt := boolToInt(b.Archived)
			var bookmarkID int64
			err = tx.QueryRowContext(
				r.Context(),
				`SELECT id FROM bookmarks WHERE group_id = ? AND url = ? LIMIT 1`,
				groupID,
				urlValue,
			).Scan(&bookmarkID)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.ExecContext(
					r.Context(),
					`INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					groupID,
					title,
					urlValue,
					notes,
					tagsRaw,
					favoriteInt,
					pinnedInt,
					archivedInt,
					b.SortOrder,
					remindAt,
				); err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
				createdBookmarks++
			} else if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			} else {
				if _, err := tx.ExecContext(
					r.Context(),
					`UPDATE bookmarks SET title = ?, notes = ?, tags = ?, favorite = ?, pinned = ?, archived = ?, sort_order = ?, remind_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
					title,
					notes,
					tagsRaw,
					favoriteInt,
					pinnedInt,
					archivedInt,
					b.SortOrder,
					remindAt,
					bookmarkID,
				); err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
				updatedBookmarks++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rollback = false

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"imported_groups":   len(payload.Groups),
		"created_groups":    createdGroups,
		"created_bookmarks": createdBookmarks,
		"updated_bookmarks": updatedBookmarks,
	})
}

func (app *application) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	groups, err := app.fetchState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	bkGroups := make([]backupGroup, 0, len(groups))
	for _, g := range groups {
		bg := backupGroup{
			ID:          g.ID,
			Name:        g.Name,
			Description: g.Description,
			SortOrder:   g.SortOrder,
			Bookmarks:   make([]backupBookmark, 0, len(g.Bookmarks)),
		}
		for _, b := range g.Bookmarks {
			bg.Bookmarks = append(bg.Bookmarks, backupBookmark{
				ID:        b.ID,
				Title:     b.Title,
				URL:       b.URL,
				Notes:     b.Notes,
				Tags:      b.Tags,
				Favorite:  b.Favorite,
				Pinned:    b.Pinned,
				Archived:  b.Archived,
				SortOrder: b.SortOrder,
				RemindAt:  b.RemindAt,
			})
		}
		bkGroups = append(bkGroups, bg)
	}

	rows, err := app.db.QueryContext(r.Context(),
		`SELECT title, content, type, bookmark_ids, tags FROM notes ORDER BY id ASC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	bkNotes := make([]backupNote, 0)
	for rows.Next() {
		var n backupNote
		var bmRaw, tagsRaw string
		if err := rows.Scan(&n.Title, &n.Content, &n.Type, &bmRaw, &tagsRaw); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if n.Type == "vault" && !strings.HasPrefix(n.Content, "vault:v1:") {
			n.Content = ""
		}
		n.BookmarkIDs = parseBookmarkIDs(bmRaw)
		n.Tags = parseTags(tagsRaw)
		bkNotes = append(bkNotes, n)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	payload := backupPayload{
		Version:    1,
		ExportedAt: time.Now(),
		Groups:     bkGroups,
		Notes:      bkNotes,
	}

	stamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("lesezeichen-vollsicherung-%s.json", stamp)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode backup: %v", err)
	}
}

func (app *application) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	limitedBody := io.LimitReader(r.Body, 20*1024*1024)
	defer r.Body.Close()
	bodyBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("request body konnte nicht gelesen werden"))
		return
	}

	var payload backupPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	if payload.Version != 1 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unbekannte backup-version: %d", payload.Version))
		return
	}
	preview, err := app.buildRestorePreview(r.Context(), payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if r.URL.Query().Get("preview") == "1" {
		writeJSON(w, http.StatusOK, preview)
		return
	}
	if !preview.Valid {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("backup ist ungültig: %s", strings.Join(preview.Errors, "; ")))
		return
	}
	conflictStrategy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("conflicts")))
	if conflictStrategy == "" {
		conflictStrategy = "overwrite"
	}
	if conflictStrategy != "overwrite" && conflictStrategy != "skip" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungültige konfliktstrategie"))
		return
	}

	tx, err := app.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	// old bookmark ID → new bookmark ID (for remapping note links)
	bookmarkIDMap := make(map[int64]int64)
	createdGroups, createdBookmarks, updatedBookmarks, createdNotes, updatedNotes := 0, 0, 0, 0, 0

	for _, g := range payload.Groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}

		var groupID int64
		err := tx.QueryRowContext(r.Context(),
			`SELECT id FROM groups WHERE lower(name) = lower(?) LIMIT 1`, name).Scan(&groupID)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(r.Context(),
				`INSERT INTO groups(name, description, sort_order) VALUES(?, ?, ?)`,
				name, strings.TrimSpace(g.Description), g.SortOrder)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			groupID, _ = res.LastInsertId()
			createdGroups++
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else if conflictStrategy == "overwrite" {
			_, _ = tx.ExecContext(r.Context(),
				`UPDATE groups SET description = ?, sort_order = ? WHERE id = ?`,
				strings.TrimSpace(g.Description), g.SortOrder, groupID)
		}

		for _, b := range g.Bookmarks {
			title := strings.TrimSpace(b.Title)
			urlValue := strings.TrimSpace(b.URL)
			if title == "" || urlValue == "" {
				continue
			}
			tagsRaw := serializeTags(b.Tags)

			var newID int64
			err := tx.QueryRowContext(r.Context(),
				`SELECT id FROM bookmarks WHERE group_id = ? AND url = ? LIMIT 1`,
				groupID, urlValue).Scan(&newID)
			if errors.Is(err, sql.ErrNoRows) {
				res, err := tx.ExecContext(r.Context(),
					`INSERT INTO bookmarks(group_id, title, url, notes, tags, favorite, pinned, archived, sort_order, remind_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					groupID, title, urlValue, b.Notes, tagsRaw,
					boolToInt(b.Favorite), boolToInt(b.Pinned), boolToInt(b.Archived),
					b.SortOrder, b.RemindAt)
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err)
					return
				}
				newID, _ = res.LastInsertId()
				createdBookmarks++
			} else if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			} else if conflictStrategy == "overwrite" {
				_, _ = tx.ExecContext(r.Context(),
					`UPDATE bookmarks SET title = ?, notes = ?, tags = ?, favorite = ?, pinned = ?, archived = ?, sort_order = ?, remind_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
					title, b.Notes, tagsRaw,
					boolToInt(b.Favorite), boolToInt(b.Pinned), boolToInt(b.Archived),
					b.SortOrder, b.RemindAt, newID)
				updatedBookmarks++
			}

			if b.ID > 0 {
				bookmarkIDMap[b.ID] = newID
			}
		}
	}

	for _, n := range payload.Notes {
		title := strings.TrimSpace(n.Title)
		if title == "" {
			continue
		}
		noteType := n.Type
		if noteType != "note" && noteType != "code" && noteType != "annotation" && noteType != "vault" {
			noteType = "note"
		}

		// Remap old bookmark IDs → new IDs; drop any that no longer exist
		remapped := make([]int64, 0, len(n.BookmarkIDs))
		for _, oldID := range n.BookmarkIDs {
			if newID, ok := bookmarkIDMap[oldID]; ok {
				remapped = append(remapped, newID)
			}
		}

		bmRaw := serializeBookmarkIDs(remapped)
		tagsRaw := serializeTags(n.Tags)

		var existingID int64
		err := tx.QueryRowContext(r.Context(),
			`SELECT id FROM notes WHERE title = ? LIMIT 1`, title).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(r.Context(),
				`INSERT INTO notes(title, content, type, bookmark_ids, tags) VALUES(?, ?, ?, ?, ?)`,
				title, strings.TrimSpace(n.Content), noteType, bmRaw, tagsRaw)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			createdNotes++
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else if conflictStrategy == "overwrite" {
			_, _ = tx.ExecContext(r.Context(),
				`UPDATE notes SET content = ?, type = ?, bookmark_ids = ?, tags = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				strings.TrimSpace(n.Content), noteType, bmRaw, tagsRaw, existingID)
			updatedNotes++
		}
	}

	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rollback = false

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"created_groups":    createdGroups,
		"created_bookmarks": createdBookmarks,
		"updated_bookmarks": updatedBookmarks,
		"created_notes":     createdNotes,
		"updated_notes":     updatedNotes,
		"conflict_strategy": conflictStrategy,
	})
}

func (app *application) buildRestorePreview(ctx context.Context, payload backupPayload) (restorePreview, error) {
	preview := restorePreview{Valid: true, Errors: make([]string, 0), Conflicts: make([]string, 0)}
	for groupIndex, backupGroup := range payload.Groups {
		groupName := strings.TrimSpace(backupGroup.Name)
		if groupName == "" {
			preview.Errors = append(preview.Errors, fmt.Sprintf("Gruppe %d hat keinen Namen", groupIndex+1))
			continue
		}

		var groupID int64
		err := app.db.QueryRowContext(ctx, `SELECT id FROM groups WHERE lower(name) = lower(?) LIMIT 1`, groupName).Scan(&groupID)
		groupExists := err == nil
		if errors.Is(err, sql.ErrNoRows) {
			preview.NewGroups++
		} else if err != nil {
			return preview, err
		} else {
			preview.ExistingGroups++
			preview.Conflicts = append(preview.Conflicts, fmt.Sprintf("Gruppe: %s", groupName))
		}

		for bookmarkIndex, backupBookmark := range backupGroup.Bookmarks {
			title := strings.TrimSpace(backupBookmark.Title)
			urlValue := strings.TrimSpace(backupBookmark.URL)
			if title == "" {
				preview.Errors = append(preview.Errors, fmt.Sprintf("Lesezeichen %d in %s hat keinen Titel", bookmarkIndex+1, groupName))
			}
			if err := validateURL(urlValue); err != nil {
				preview.Errors = append(preview.Errors, fmt.Sprintf("Lesezeichen %q in %s: %v", title, groupName, err))
			}
			if !groupExists || title == "" || urlValue == "" {
				preview.NewBookmarks++
				continue
			}
			var bookmarkID int64
			err := app.db.QueryRowContext(ctx, `SELECT id FROM bookmarks WHERE group_id = ? AND url = ? LIMIT 1`, groupID, urlValue).Scan(&bookmarkID)
			if errors.Is(err, sql.ErrNoRows) {
				preview.NewBookmarks++
			} else if err != nil {
				return preview, err
			} else {
				preview.ConflictingBookmarks++
				preview.Conflicts = append(preview.Conflicts, fmt.Sprintf("Lesezeichen: %s / %s", groupName, title))
			}
		}
	}

	for noteIndex, backupNote := range payload.Notes {
		title := strings.TrimSpace(backupNote.Title)
		if title == "" {
			preview.Errors = append(preview.Errors, fmt.Sprintf("Notiz %d hat keinen Titel", noteIndex+1))
			continue
		}
		var noteID int64
		err := app.db.QueryRowContext(ctx, `SELECT id FROM notes WHERE title = ? LIMIT 1`, title).Scan(&noteID)
		if errors.Is(err, sql.ErrNoRows) {
			preview.NewNotes++
		} else if err != nil {
			return preview, err
		} else {
			preview.ConflictingNotes++
			preview.Conflicts = append(preview.Conflicts, fmt.Sprintf("Notiz: %s", title))
		}
	}
	preview.Valid = len(preview.Errors) == 0
	return preview, nil
}

func filterGroupsByID(groups []group, groupID int64) []group {
	out := make([]group, 0, 1)
	for _, g := range groups {
		if g.ID == groupID {
			out = append(out, g)
			break
		}
	}
	return out
}

func parseOptionalDateTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}

	return nil, fmt.Errorf("invalid datetime")
}

func formatOptionalDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func writeCSVExport(w io.Writer, groups []group) error {
	cw := csv.NewWriter(w)
	header := []string{
		"group_id",
		"group_name",
		"group_description",
		"bookmark_id",
		"title",
		"url",
		"notes",
		"tags",
		"favorite",
		"pinned",
		"archived",
		"sort_order",
		"remind_at",
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, g := range groups {
		for _, b := range g.Bookmarks {
			record := []string{
				strconv.FormatInt(g.ID, 10),
				g.Name,
				g.Description,
				strconv.FormatInt(b.ID, 10),
				b.Title,
				b.URL,
				b.Notes,
				strings.Join(b.Tags, ","),
				strconv.FormatBool(b.Favorite),
				strconv.FormatBool(b.Pinned),
				strconv.FormatBool(b.Archived),
				strconv.Itoa(b.SortOrder),
				formatOptionalDate(b.RemindAt),
			}
			if err := cw.Write(record); err != nil {
				return err
			}
		}
	}

	cw.Flush()
	return cw.Error()
}

func writeHTMLExport(w io.Writer, groups []group) error {
	tpl := `<!doctype html>
<html lang="de">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Lesezeichen Startseite</title>
  <style>
    :root { --bg:#f6f7fb; --ink:#142033; --muted:#5a687d; --card:#fff; --line:#dbe2ee; --accent:#0f8e74; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: "Segoe UI", "Avenir Next", sans-serif; background: var(--bg); color: var(--ink); }
    .wrap { max-width: 1200px; margin: 0 auto; padding: 24px 16px 40px; }
    h1 { margin: 0 0 6px; }
    .hint { margin: 0 0 20px; color: var(--muted); }
    .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 12px; }
    .group { background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 12px; }
    .group h2 { margin: 0; font-size: 1.08rem; }
    .desc { margin: 4px 0 10px; color: var(--muted); font-size: 0.9rem; }
    ul { margin: 0; padding-left: 18px; display: grid; gap: 6px; }
    a { color: var(--accent); text-decoration: none; }
    a:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <main class="wrap">
    <h1>Lesezeichen Startseite</h1>
    <p class="hint">Generiert am {{ .GeneratedAt }}</p>
    <section class="grid">
      {{ range .Groups }}
      <article class="group">
        <h2>{{ .Name }}</h2>
        {{ if .Description }}<p class="desc">{{ .Description }}</p>{{ end }}
        <ul>
          {{ range .Bookmarks }}
          <li><a href="{{ .URL }}" target="_blank" rel="noreferrer">{{ .Title }}</a></li>
          {{ end }}
        </ul>
      </article>
      {{ end }}
    </section>
  </main>
</body>
</html>`

	t, err := template.New("export").Parse(tpl)
	if err != nil {
		return err
	}

	data := struct {
		GeneratedAt string
		Groups      []group
	}{
		GeneratedAt: time.Now().Format("02.01.2006 15:04"),
		Groups:      groups,
	}

	return t.Execute(w, data)
}
