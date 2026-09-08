package main

import (
	"database/sql"
	"strings"
)

func initializeSchema(db *sql.DB) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`PRAGMA synchronous = NORMAL;`,
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS bookmarks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			notes TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE SET NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_id ON bookmarks(group_id);`,
		`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'note',
			bookmark_ids TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS modules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			root_path TEXT NOT NULL,
			source_url TEXT NOT NULL DEFAULT '',
			installed_version TEXT NOT NULL DEFAULT '',
			managed INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS silver_price_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetched_at DATETIME NOT NULL UNIQUE,
			eur_per_g REAL NOT NULL,
			best_eur_per_ounce REAL NOT NULL,
			best_product_name TEXT NOT NULL DEFAULT '',
			best_product_url TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_silver_price_history_fetched_at ON silver_price_history(fetched_at);`,
		`CREATE TABLE IF NOT EXISTS http_monitor_targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			module_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			interval_seconds INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_checked_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(module_id) REFERENCES modules(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_http_monitor_targets_due ON http_monitor_targets(enabled, last_checked_at);`,
		`CREATE TABLE IF NOT EXISTS http_monitor_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id INTEGER NOT NULL,
			checked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ok INTEGER NOT NULL,
			latency_ms INTEGER NULL,
			message TEXT NOT NULL,
			FOREIGN KEY(target_id) REFERENCES http_monitor_targets(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_http_monitor_results_target_checked ON http_monitor_results(target_id, checked_at);`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	migrations := []string{
		`ALTER TABLE bookmarks ADD COLUMN tags TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE bookmarks ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN remind_at DATETIME NULL`,
		`ALTER TABLE bookmarks ADD COLUMN open_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE bookmarks ADD COLUMN last_opened_at DATETIME NULL`,
		`ALTER TABLE modules ADD COLUMN managed INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE modules ADD COLUMN installed_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE modules ADD COLUMN source_url TEXT NOT NULL DEFAULT ''`,
	}

	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}

	if err := migrateBookmarksGroupIDNullable(db); err != nil {
		return err
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_id ON bookmarks(group_id);`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_sort ON bookmarks(group_id, pinned DESC, sort_order ASC, id ASC);`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE bookmarks SET group_id = NULL WHERE group_id NOT IN (SELECT id FROM groups)`); err != nil {
		return err
	}

	return nil
}

func migrateBookmarksGroupIDNullable(db *sql.DB) error {
	needsMigration := false

	rows, err := db.Query(`PRAGMA table_info(bookmarks)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "group_id" && notNull == 1 {
			needsMigration = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	fkRows, err := db.Query(`PRAGMA foreign_key_list(bookmarks)`)
	if err != nil {
		return err
	}
	for fkRows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			fkRows.Close()
			return err
		}
		if table == "groups" && from == "group_id" && strings.ToUpper(onDelete) != "SET NULL" {
			needsMigration = true
		}
	}
	if err := fkRows.Err(); err != nil {
		fkRows.Close()
		return err
	}
	fkRows.Close()

	if !needsMigration {
		return nil
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE bookmarks RENAME TO bookmarks_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE bookmarks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		notes TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		tags TEXT NOT NULL DEFAULT '',
		favorite INTEGER NOT NULL DEFAULT 0,
		pinned INTEGER NOT NULL DEFAULT 0,
		sort_order INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		remind_at DATETIME NULL,
		open_count INTEGER NOT NULL DEFAULT 0,
		last_opened_at DATETIME NULL,
		FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE SET NULL
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO bookmarks (
		id, group_id, title, url, notes, created_at, updated_at, tags, favorite, pinned,
		sort_order, archived, remind_at, open_count, last_opened_at
	)
	SELECT
		id,
		CASE WHEN group_id IN (SELECT id FROM groups) THEN group_id ELSE NULL END,
		title, url, notes, created_at, updated_at, tags, favorite, pinned,
		sort_order, archived, remind_at, open_count, last_opened_at
	FROM bookmarks_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE bookmarks_old`); err != nil {
		return err
	}

	return tx.Commit()
}

func databaseDSN(path string) string {
	if strings.Contains(path, "?") {
		return path + "&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	return path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}
