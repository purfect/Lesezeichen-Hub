package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:C:/Users/trbr/Documents/tmp/lezezeichen/data.db?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var fk int
	_ = db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	fmt.Printf("PRAGMA foreign_keys (frische Verbindung) = %d\n\n", fk)

	fmt.Println("=== modules ===")
	rows, err := db.Query(`SELECT id, name, root_path FROM modules ORDER BY id`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var name, root string
		if err := rows.Scan(&id, &name, &root); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  id=%d name=%q root=%q\n", id, name, root)
	}
	rows.Close()

	fmt.Println("\n=== bookmarks mit /modules/ ===")
	rows, err = db.Query(`
		SELECT b.id, b.group_id, COALESCE(g.name,'<<GRUPPE FEHLT>>'), b.title, b.url, b.archived, b.pinned, b.favorite, b.sort_order
		FROM bookmarks b LEFT JOIN groups g ON g.id = b.group_id
		WHERE b.url LIKE '/modules/%' ORDER BY b.id`)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var id, groupID int64
		var groupName, title, url string
		var archived, pinned, favorite, sortOrder int
		if err := rows.Scan(&id, &groupID, &groupName, &title, &url, &archived, &pinned, &favorite, &sortOrder); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  id=%d group=%d(%s) archived=%d pinned=%d fav=%d sort=%d title=%q url=%q\n",
			id, groupID, groupName, archived, pinned, favorite, sortOrder, title, url)
	}
	rows.Close()

	fmt.Println("\n=== verwaiste Lesezeichen ===")
	rows, err = db.Query(`
		SELECT b.id, b.group_id, b.title, b.url, b.archived
		FROM bookmarks b LEFT JOIN groups g ON g.id = b.group_id
		WHERE g.id IS NULL ORDER BY b.id`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	orphans := 0
	for rows.Next() {
		var id, groupID int64
		var title, url string
		var archived int
		if err := rows.Scan(&id, &groupID, &title, &url, &archived); err != nil {
			log.Fatal(err)
		}
		orphans++
		fmt.Printf("  id=%d group_id=%d(fehlt) archived=%d title=%q url=%q\n", id, groupID, archived, title, url)
	}
	fmt.Printf("\nverwaiste Lesezeichen gesamt: %d\n", orphans)
}
