package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	autoBackupPrefix   = "lesezeichen-auto-"
	autoBackupInterval = 24 * time.Hour
	defaultBackupKeep  = 14
	maxBackupKeep      = 365
)

var autoBackupFilePattern = regexp.MustCompile(`^lesezeichen-auto-\d{8}-\d{6}\.json$`)

type backupSettings struct {
	Enabled bool   `json:"enabled"`
	Dir     string `json:"dir"`
	Keep    int    `json:"keep"`
}

type autoBackupFile struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

func (app *application) defaultBackupDir() string {
	return filepath.Join(filepath.Dir(app.dbPath), "backups")
}

func (app *application) loadBackupSettings() backupSettings {
	settings := backupSettings{Enabled: true, Dir: app.defaultBackupDir(), Keep: defaultBackupKeep}
	if value := readAppSetting(app.db, "backup_enabled"); value != "" {
		settings.Enabled = value != "false"
	}
	if value := readAppSetting(app.db, "backup_dir"); value != "" {
		settings.Dir = value
	}
	if value, err := strconv.Atoi(readAppSetting(app.db, "backup_keep")); err == nil && value > 0 && value <= maxBackupKeep {
		settings.Keep = value
	}
	return settings
}

func (app *application) saveBackupSettings(ctx context.Context, settings backupSettings) error {
	values := map[string]string{
		"backup_enabled": strconv.FormatBool(settings.Enabled),
		"backup_dir":     settings.Dir,
		"backup_keep":    strconv.Itoa(settings.Keep),
	}
	tx, err := app.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// listAutoBackups returns only files created by the scheduler, newest first.
func listAutoBackups(dir string) ([]autoBackupFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []autoBackupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]autoBackupFile, 0)
	for _, entry := range entries {
		if entry.IsDir() || !autoBackupFilePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		created, err := time.ParseInLocation("20060102-150405", strings.TrimSuffix(strings.TrimPrefix(entry.Name(), autoBackupPrefix), ".json"), time.Local)
		if err != nil {
			continue
		}
		files = append(files, autoBackupFile{Name: entry.Name(), Size: info.Size(), CreatedAt: created})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	return files, nil
}

func (app *application) createAutoBackup(ctx context.Context, settings backupSettings) (autoBackupFile, error) {
	app.backupMu.Lock()
	defer app.backupMu.Unlock()

	file, err := app.writeAutoBackup(ctx, settings)
	if err != nil {
		app.backupLastErr = err.Error()
		return autoBackupFile{}, err
	}
	app.backupLastErr = ""
	return file, nil
}

func (app *application) writeAutoBackup(ctx context.Context, settings backupSettings) (autoBackupFile, error) {
	payload, err := app.buildBackupPayload(ctx)
	if err != nil {
		return autoBackupFile{}, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return autoBackupFile{}, err
	}
	if err := os.MkdirAll(settings.Dir, 0o700); err != nil {
		return autoBackupFile{}, fmt.Errorf("sicherungsordner kann nicht angelegt werden: %w", err)
	}

	now := time.Now()
	name := autoBackupPrefix + now.Format("20060102-150405") + ".json"
	target := filepath.Join(settings.Dir, name)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return autoBackupFile{}, fmt.Errorf("sicherung kann nicht geschrieben werden: %w", err)
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return autoBackupFile{}, fmt.Errorf("sicherung kann nicht gespeichert werden: %w", err)
	}

	files, err := listAutoBackups(settings.Dir)
	if err != nil {
		return autoBackupFile{}, err
	}
	for _, old := range files[min(settings.Keep, len(files)):] {
		if err := os.Remove(filepath.Join(settings.Dir, old.Name)); err != nil {
			log.Printf("alte sicherung %s nicht geloescht: %v", old.Name, err)
		}
	}
	return autoBackupFile{Name: name, Size: int64(len(data)), CreatedAt: now}, nil
}

func (app *application) runDueAutoBackup(ctx context.Context) {
	settings := app.loadBackupSettings()
	if !settings.Enabled {
		return
	}
	files, err := listAutoBackups(settings.Dir)
	if err == nil && len(files) > 0 && time.Since(files[0].CreatedAt) < autoBackupInterval {
		return
	}
	if _, err := app.createAutoBackup(ctx, settings); err != nil {
		log.Printf("automatische sicherung: %v", err)
	}
}

func (app *application) runAutoBackupScheduler(ctx context.Context) {
	app.runDueAutoBackup(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.runDueAutoBackup(ctx)
		}
	}
}

func (app *application) writeBackupStatus(w http.ResponseWriter, settings backupSettings) {
	files, err := listAutoBackups(settings.Dir)
	lastErr := ""
	if err != nil {
		lastErr = err.Error()
		files = []autoBackupFile{}
	}
	app.backupMu.Lock()
	if app.backupLastErr != "" {
		lastErr = app.backupLastErr
	}
	app.backupMu.Unlock()

	if absolute, err := filepath.Abs(settings.Dir); err == nil {
		settings.Dir = absolute
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    settings.Enabled,
		"dir":        settings.Dir,
		"keep":       settings.Keep,
		"backups":    files,
		"last_error": lastErr,
	})
}

func (app *application) handleBackupSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		app.writeBackupStatus(w, app.loadBackupSettings())

	case http.MethodPut:
		// a JSON content type forces a CORS preflight, so other websites cannot change the target folder
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeErr(w, http.StatusUnsupportedMediaType, fmt.Errorf("content-type application/json erforderlich"))
			return
		}
		var input backupSettings
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}
		input.Dir = strings.TrimSpace(input.Dir)
		if input.Dir == "" {
			input.Dir = app.defaultBackupDir()
		}
		if input.Keep < 1 || input.Keep > maxBackupKeep {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("anzahl der sicherungen muss zwischen 1 und %d liegen", maxBackupKeep))
			return
		}
		if info, err := os.Stat(input.Dir); err == nil && !info.IsDir() {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("sicherungsziel ist kein ordner"))
			return
		}
		if err := app.saveBackupSettings(r.Context(), input); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		app.backupMu.Lock()
		app.backupLastErr = ""
		app.backupMu.Unlock()
		app.writeBackupStatus(w, input)

	default:
		methodNotAllowed(w)
	}
}

func (app *application) handleBackupRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	settings := app.loadBackupSettings()
	if _, err := app.createAutoBackup(r.Context(), settings); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	app.writeBackupStatus(w, settings)
}
