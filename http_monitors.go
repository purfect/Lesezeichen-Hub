package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxHTTPMonitorInterval = 24 * 60 * 60

type httpMonitorTarget struct {
	ID              int64      `json:"id"`
	ModuleID        int64      `json:"module_id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	IntervalSeconds int        `json:"interval_seconds"`
	Enabled         bool       `json:"enabled"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
}

type httpMonitorResult struct {
	ID        int64     `json:"id"`
	TargetID  int64     `json:"target_id"`
	CheckedAt time.Time `json:"checked_at"`
	OK        bool      `json:"ok"`
	LatencyMS *int64    `json:"latency_ms,omitempty"`
	Message   string    `json:"message"`
}

func (app *application) handleHTTPMonitors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		moduleID, err := httpMonitorModuleID(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		rows, err := app.db.QueryContext(r.Context(), `SELECT id, module_id, name, url, interval_seconds, enabled, last_checked_at FROM http_monitor_targets WHERE module_id = ? ORDER BY id`, moduleID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()
		targets := make([]httpMonitorTarget, 0)
		for rows.Next() {
			var target httpMonitorTarget
			if err := rows.Scan(&target.ID, &target.ModuleID, &target.Name, &target.URL, &target.IntervalSeconds, &target.Enabled, &target.LastCheckedAt); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			targets = append(targets, target)
		}
		writeJSON(w, http.StatusOK, targets)
	case http.MethodPost:
		var input struct {
			ModuleID        int64  `json:"module_id"`
			Name            string `json:"name"`
			URL             string `json:"url"`
			IntervalSeconds int    `json:"interval_seconds"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
			return
		}
		if err := validateHTTPMonitorInput(input.ModuleID, input.Name, input.URL, input.IntervalSeconds); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		result, err := app.db.ExecContext(r.Context(), `INSERT INTO http_monitor_targets(module_id, name, url, interval_seconds) VALUES(?, ?, ?, ?)`, input.ModuleID, strings.TrimSpace(input.Name), strings.TrimSpace(input.URL), input.IntervalSeconds)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("monitorziel konnte nicht angelegt werden: %w", err))
			return
		}
		id, _ := result.LastInsertId()
		writeJSON(w, http.StatusCreated, httpMonitorTarget{ID: id, ModuleID: input.ModuleID, Name: strings.TrimSpace(input.Name), URL: strings.TrimSpace(input.URL), IntervalSeconds: input.IntervalSeconds, Enabled: true})
	default:
		methodNotAllowed(w)
	}
}

func (app *application) handleHTTPMonitorRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/http-monitors/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("monitor-id fehlt"))
		return
	}
	targetID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || targetID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("monitor-id ist ungueltig"))
		return
	}
	moduleID, err := httpMonitorModuleID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(parts) == 2 && parts[1] == "check" && r.Method == http.MethodPost {
		result, err := app.checkAndStoreHTTPMonitor(r.Context(), targetID, moduleID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		result, err := app.db.ExecContext(r.Context(), `DELETE FROM http_monitor_targets WHERE id = ? AND module_id = ?`, targetID, moduleID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			writeErr(w, http.StatusNotFound, fmt.Errorf("monitorziel nicht gefunden"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	methodNotAllowed(w)
}

func (app *application) handleHTTPMonitorResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	moduleID, err := httpMonitorModuleID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	targetID, err := parseOptionalPositiveInt64(r.URL.Query().Get("target_id"))
	if err != nil || targetID == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("target_id ist erforderlich"))
		return
	}
	rows, err := app.db.QueryContext(r.Context(), `SELECT r.id, r.target_id, r.checked_at, r.ok, r.latency_ms, r.message FROM http_monitor_results r JOIN http_monitor_targets t ON t.id = r.target_id WHERE r.target_id = ? AND t.module_id = ? ORDER BY r.checked_at ASC LIMIT 200`, targetID, moduleID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	results := make([]httpMonitorResult, 0)
	for rows.Next() {
		var result httpMonitorResult
		if err := rows.Scan(&result.ID, &result.TargetID, &result.CheckedAt, &result.OK, &result.LatencyMS, &result.Message); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
}

func (app *application) runHTTPMonitorScheduler(ctx context.Context) {
	app.runDueHTTPMonitors(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.runDueHTTPMonitors(ctx)
		}
	}
}

func (app *application) runDueHTTPMonitors(ctx context.Context) {
	rows, err := app.db.QueryContext(ctx, `SELECT id, module_id, interval_seconds, last_checked_at FROM http_monitor_targets WHERE enabled = 1 AND interval_seconds > 0`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var targetID, moduleID int64
		var interval int
		var lastCheckedAt *time.Time
		if rows.Scan(&targetID, &moduleID, &interval, &lastCheckedAt) != nil || (lastCheckedAt != nil && time.Since(*lastCheckedAt) < time.Duration(interval)*time.Second) {
			continue
		}
		_, _ = app.checkAndStoreHTTPMonitor(ctx, targetID, moduleID)
	}
}

func (app *application) checkAndStoreHTTPMonitor(ctx context.Context, targetID, moduleID int64) (httpMonitorResult, error) {
	var targetURL string
	if err := app.db.QueryRowContext(ctx, `SELECT url FROM http_monitor_targets WHERE id = ? AND module_id = ?`, targetID, moduleID).Scan(&targetURL); err != nil {
		if err == sql.ErrNoRows {
			return httpMonitorResult{}, fmt.Errorf("monitorziel nicht gefunden")
		}
		return httpMonitorResult{}, err
	}
	result := checkPublicHTTPURL(ctx, targetURL)
	result.TargetID = targetID
	if _, err := app.db.ExecContext(ctx, `INSERT INTO http_monitor_results(target_id, checked_at, ok, latency_ms, message) VALUES(?, ?, ?, ?, ?)`, targetID, result.CheckedAt, result.OK, result.LatencyMS, result.Message); err != nil {
		return httpMonitorResult{}, err
	}
	if _, err := app.db.ExecContext(ctx, `UPDATE http_monitor_targets SET last_checked_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, result.CheckedAt, targetID); err != nil {
		return httpMonitorResult{}, err
	}
	return result, nil
}

func checkPublicHTTPURL(ctx context.Context, rawURL string) httpMonitorResult {
	result := httpMonitorResult{CheckedAt: time.Now().UTC()}
	target, err := publicHTTPURL(rawURL)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	started := time.Now()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	request.Header.Set("User-Agent", "Lesezeichen-Hub-HTTP-Monitor/1.0")
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: publicDialContext}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	latency := time.Since(started).Milliseconds()
	result.LatencyMS = &latency
	if err != nil {
		result.Message = "Netzwerkfehler"
		return result
	}
	defer response.Body.Close()
	result.OK = response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest
	result.Message = fmt.Sprintf("HTTP %d", response.StatusCode)
	return result
}

func httpMonitorModuleID(r *http.Request) (int64, error) {
	moduleID, err := parseOptionalPositiveInt64(r.URL.Query().Get("module_id"))
	if err != nil || moduleID == 0 {
		return 0, fmt.Errorf("module_id ist erforderlich")
	}
	return moduleID, nil
}

func validateHTTPMonitorInput(moduleID int64, name, rawURL string, interval int) error {
	if moduleID <= 0 || strings.TrimSpace(name) == "" {
		return fmt.Errorf("module_id und name sind erforderlich")
	}
	if _, err := publicHTTPURL(rawURL); err != nil {
		return err
	}
	if interval < 0 || interval > maxHTTPMonitorInterval {
		return fmt.Errorf("intervall muss zwischen 0 und %d Sekunden liegen", maxHTTPMonitorInterval)
	}
	return nil
}

func publicHTTPURL(raw string) (*url.URL, error) {
	target, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || target.Hostname() == "" {
		return nil, fmt.Errorf("url ist ungueltig")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("nur http:// und https:// sind erlaubt")
	}
	if ip, err := netip.ParseAddr(target.Hostname()); err == nil && !isPublicIP(ip) {
		return nil, fmt.Errorf("lokale und private netzadressen sind nicht erlaubt")
	}
	return target, nil
}

func publicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if !isPublicIP(address) {
			return nil, fmt.Errorf("lokale und private netzadressen sind nicht erlaubt")
		}
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func isPublicIP(ip netip.Addr) bool {
	return !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}
