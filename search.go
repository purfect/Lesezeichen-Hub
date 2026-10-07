package main

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Snippet markers char(2)/char(3) let the client escape the text first and then insert <mark>.
const (
	ftsSnippetSQL   = `snippet(notes_fts, -1, char(2), char(3), '…', 64)`
	ftsNotesRankSQL = `bm25(notes_fts, 10.0, 1.0, 5.0)`
)

// The trigram tokenizer keeps the previous substring search behaviour ("hub" finds "GitHub").
func initializeSearchIndex(db *sql.DB) error {
	statements := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS bookmarks_fts USING fts5(title, url, notes, tags, tokenize='trigram remove_diacritics 1')`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(title, content, tags, tokenize='trigram remove_diacritics 1')`,
		`CREATE TRIGGER IF NOT EXISTS bookmarks_fts_ai AFTER INSERT ON bookmarks BEGIN
			INSERT INTO bookmarks_fts(rowid, title, url, notes, tags) VALUES (new.id, new.title, new.url, new.notes, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS bookmarks_fts_ad AFTER DELETE ON bookmarks BEGIN
			DELETE FROM bookmarks_fts WHERE rowid = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS bookmarks_fts_au AFTER UPDATE OF title, url, notes, tags ON bookmarks BEGIN
			DELETE FROM bookmarks_fts WHERE rowid = old.id;
			INSERT INTO bookmarks_fts(rowid, title, url, notes, tags) VALUES (new.id, new.title, new.url, new.notes, new.tags);
		END`,
		// vault content is encrypted, indexing it would only produce noise
		`CREATE TRIGGER IF NOT EXISTS notes_fts_ai AFTER INSERT ON notes BEGIN
			INSERT INTO notes_fts(rowid, title, content, tags)
			VALUES (new.id, new.title, CASE WHEN new.type = 'vault' THEN '' ELSE new.content END, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_ad AFTER DELETE ON notes BEGIN
			DELETE FROM notes_fts WHERE rowid = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_au AFTER UPDATE OF title, content, tags, type ON notes BEGIN
			DELETE FROM notes_fts WHERE rowid = old.id;
			INSERT INTO notes_fts(rowid, title, content, tags)
			VALUES (new.id, new.title, CASE WHEN new.type = 'vault' THEN '' ELSE new.content END, new.tags);
		END`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	// rebuilding on startup keeps the index consistent after table migrations or external edits
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rebuild := []string{
		`DELETE FROM bookmarks_fts`,
		`INSERT INTO bookmarks_fts(rowid, title, url, notes, tags) SELECT id, title, url, notes, tags FROM bookmarks`,
		`DELETE FROM notes_fts`,
		`INSERT INTO notes_fts(rowid, title, content, tags)
			SELECT id, title, CASE WHEN type = 'vault' THEN '' ELSE content END, tags FROM notes`,
	}
	for _, stmt := range rebuild {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// refreshSearchIndex reconciles the FTS tables with their source tables before
// searching. Triggers normally keep them in sync; this also catches writes made
// by imports or external tools that bypassed those triggers.
func refreshSearchIndex(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`DELETE FROM bookmarks_fts
			WHERE rowid NOT IN (SELECT id FROM bookmarks)
				OR EXISTS (
					SELECT 1 FROM bookmarks b WHERE b.id = bookmarks_fts.rowid
						AND (b.title IS NOT bookmarks_fts.title OR b.url IS NOT bookmarks_fts.url
							OR b.notes IS NOT bookmarks_fts.notes OR b.tags IS NOT bookmarks_fts.tags)
				)`,
		`INSERT INTO bookmarks_fts(rowid, title, url, notes, tags)
			SELECT b.id, b.title, b.url, b.notes, b.tags FROM bookmarks b
			WHERE NOT EXISTS (SELECT 1 FROM bookmarks_fts f WHERE f.rowid = b.id)`,
		`DELETE FROM notes_fts
			WHERE rowid NOT IN (SELECT id FROM notes)
				OR EXISTS (
					SELECT 1 FROM notes n WHERE n.id = notes_fts.rowid
						AND (n.title IS NOT notes_fts.title
							OR (CASE WHEN n.type = 'vault' THEN '' ELSE n.content END) IS NOT notes_fts.content
							OR n.tags IS NOT notes_fts.tags)
				)`,
		`INSERT INTO notes_fts(rowid, title, content, tags)
			SELECT n.id, n.title, CASE WHEN n.type = 'vault' THEN '' ELSE n.content END, n.tags FROM notes n
			WHERE NOT EXISTS (SELECT 1 FROM notes_fts f WHERE f.rowid = n.id)`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ftsQuery struct {
	match string
	likes []string
}

// Terms shorter than three characters cannot be matched by the trigram index and fall back to LIKE.
func parseFTSQuery(raw string) ftsQuery {
	var q ftsQuery
	phrases := make([]string, 0)
	for _, term := range strings.Fields(raw) {
		if utf8.RuneCountInString(term) >= 3 {
			phrases = append(phrases, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
			continue
		}
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
		q.likes = append(q.likes, "%"+escaped+"%")
	}
	q.match = strings.Join(phrases, " AND ")
	return q
}

func (q ftsQuery) empty() bool {
	return q.match == "" && len(q.likes) == 0
}

func (q ftsQuery) where(table string, columns []string) (string, []any) {
	conditions := make([]string, 0, 1+len(q.likes))
	args := make([]any, 0, 1+len(q.likes)*len(columns))
	if q.match != "" {
		conditions = append(conditions, table+" MATCH ?")
		args = append(args, q.match)
	}
	for _, like := range q.likes {
		alternatives := make([]string, len(columns))
		for i, column := range columns {
			alternatives[i] = table + "." + column + ` LIKE ? ESCAPE '\'`
			args = append(args, like)
		}
		conditions = append(conditions, "("+strings.Join(alternatives, " OR ")+")")
	}
	return strings.Join(conditions, " AND "), args
}

func (app *application) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	type noteHit struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Type    string `json:"type"`
		Snippet string `json:"snippet"`
	}
	bookmarkIDs := make([]int64, 0)
	notes := make([]noteHit, 0)

	q := parseFTSQuery(r.URL.Query().Get("q"))
	if q.empty() {
		writeJSON(w, http.StatusOK, map[string]any{"bookmark_ids": bookmarkIDs, "notes": notes})
		return
	}
	if err := refreshSearchIndex(r.Context(), app.db); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	where, args := q.where("bookmarks_fts", []string{"title", "url", "notes", "tags"})
	rows, err := app.db.QueryContext(r.Context(), `SELECT rowid FROM bookmarks_fts WHERE `+where, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		bookmarkIDs = append(bookmarkIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if r.URL.Query().Get("include_notes") == "true" {
		snippet, order := "''", "n.updated_at DESC"
		if q.match != "" {
			snippet, order = ftsSnippetSQL, ftsNotesRankSQL
		}
		where, args = q.where("notes_fts", []string{"title", "content", "tags"})
		rows, err = app.db.QueryContext(r.Context(),
			`SELECT n.id, n.title, n.type, `+snippet+` FROM notes_fts JOIN notes n ON n.id = notes_fts.rowid
				 WHERE `+where+` ORDER BY `+order+` LIMIT 20`, args...)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var hit noteHit
			if err := rows.Scan(&hit.ID, &hit.Title, &hit.Type, &hit.Snippet); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			notes = append(notes, hit)
		}
		if err := rows.Err(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"bookmark_ids": bookmarkIDs, "notes": notes})
}
