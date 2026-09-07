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
			group_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			notes TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE CASCADE
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

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bookmarks_group_sort ON bookmarks(group_id, pinned DESC, sort_order ASC, id ASC);`); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM bookmarks WHERE group_id NOT IN (SELECT id FROM groups)`); err != nil {
		return err
	}

	return nil
}

func databaseDSN(path string) string {
	if strings.Contains(path, "?") {
		return path + "&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	return path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}
