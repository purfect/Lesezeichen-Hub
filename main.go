package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed web/*
var embeddedWebFiles embed.FS

func main() {
	dbPath := envOrDefault("BOOKMARK_DB_PATH", "./data.db")
	addr := envOrDefault("ADDR", "127.0.0.1:2222")

	db, err := sql.Open("sqlite", databaseDSN(dbPath))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := initializeSchema(db); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	app := &application{
		db:                 db,
		moduleAPIBase:      githubAPIBase,
		moduleWebBase:      githubWebBase,
		moduleManifestBase: githubRawBase,
		moduleInstallDir:   envOrDefault("MODULES_PATH", "./modules"),
		externalPrices:     envBool("ENABLE_EXTERNAL_PRICES", true),
	}
	mux := http.NewServeMux()

	webFS, err := fs.Sub(embeddedWebFiles, "web")
	if err != nil {
		log.Fatalf("init embedded web assets: %v", err)
	}
	app.webFS = webFS
	monitorContext, stopMonitors := context.WithCancel(context.Background())
	defer stopMonitors()
	go app.runHTTPMonitorScheduler(monitorContext)

	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		log.Fatalf("read embedded index.html: %v", err)
	}

	mux.HandleFunc("/api/state", app.handleState)
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/update/check", app.handleUpdateCheck)
	mux.HandleFunc("/api/update/install", app.handleUpdateInstall)
	mux.HandleFunc("/api/export", app.handleExport)
	mux.HandleFunc("/api/import", app.handleImport)
	mux.HandleFunc("/api/backup", app.handleBackup)
	mux.HandleFunc("/api/restore", app.handleRestore)
	mux.HandleFunc("/api/groups", app.handleGroups)
	mux.HandleFunc("/api/groups/", app.handleGroupRoutes)
	mux.HandleFunc("/api/bookmarks", app.handleBookmarks)
	mux.HandleFunc("/api/bookmarks/", app.handleBookmarkRoutes)
	mux.HandleFunc("/api/notes", app.handleNotes)
	mux.HandleFunc("/api/notes/", app.handleNoteRoutes)
	mux.HandleFunc("/api/note-groups", app.handleNoteGroups)
	mux.HandleFunc("/api/note-groups/", app.handleNoteGroupRoutes)
	mux.HandleFunc("/api/modules", app.handleModules)
	mux.HandleFunc("/api/modules/", app.handleModuleRoutes)
	mux.HandleFunc("/api/module-import", app.handleModuleImport)
	mux.HandleFunc("/api/module-catalog", app.handleModuleCatalog)
	mux.HandleFunc("/api/module-catalog/", app.handleModuleCatalogRoutes)
	mux.HandleFunc("/api/module-folder", app.handleModuleFolder)
	mux.HandleFunc("/modules/", app.handleModuleFiles)
	mux.HandleFunc("/api/metal-prices", app.handleMetalPrices)
	mux.HandleFunc("/api/silver-prices", app.handleSilverPrices)
	mux.HandleFunc("/api/silver-price-history", app.handleSilverPriceHistory)
	mux.HandleFunc("/api/silver-price-history-bounds", app.handleSilverPriceHistoryBounds)
	mux.HandleFunc("/api/http-monitors", app.handleHTTPMonitors)
	mux.HandleFunc("/api/http-monitors/", app.handleHTTPMonitorRoutes)
	mux.HandleFunc("/api/http-monitor-results", app.handleHTTPMonitorResults)
	mux.HandleFunc("/api/http/inspect", app.handleHTTPInspect)
	mux.HandleFunc("/api/redirect-inspector", app.handleHTTPInspect)
	mux.HandleFunc("/silver-preise", app.handleSilverPricesPage)
	mux.HandleFunc("/silberpreis-verlauf", app.handleSilverPriceHistoryPage)
	// embed.FS reports a fixed zero ModTime, so If-Modified-Since would otherwise hide rebuilds behind stale 304s
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(webFS)))
	mux.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		staticHandler.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(indexHTML); err != nil {
			log.Printf("write index.html: %v", err)
		}
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           recoverMiddleware(loggingMiddleware(mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Lesezeichen-Server laeuft auf http://%s", addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}
