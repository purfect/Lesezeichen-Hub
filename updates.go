package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (app *application) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	info, err := fetchLatestUpdateInfo(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, info)
}

func (app *application) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if runtime.GOOS != "windows" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("updates koennen nur unter Windows installiert werden"))
		return
	}

	info, err := fetchLatestUpdateInfo(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if !info.UpdateAvailable {
		writeJSON(w, http.StatusOK, map[string]any{"message": "keine neue version verfuegbar"})
		return
	}
	if !info.CanInstall {
		writeErr(w, http.StatusBadRequest, errors.New(info.Message))
		return
	}

	currentExe, err := os.Executable()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("pfad der laufenden exe konnte nicht ermittelt werden"))
		return
	}
	currentExe, err = filepath.Abs(currentExe)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("pfad der laufenden exe ist ungueltig"))
		return
	}
	if !strings.EqualFold(filepath.Ext(currentExe), ".exe") {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("update-installation ist nur aus einer windows-exe moeglich"))
		return
	}

	appDir := filepath.Dir(currentExe)
	runtimeDir := filepath.Join(appDir, ".runtime")
	updatesDir := filepath.Join(runtimeDir, "updates")
	if err := os.MkdirAll(updatesDir, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("update-ordner konnte nicht erstellt werden: %w", err))
		return
	}

	targetPath := filepath.Join(updatesDir, info.AssetName)
	if err := downloadFile(r.Context(), info.AssetURL, targetPath); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := verifyDownloadedAsset(r.Context(), info.ChecksumURL, targetPath); err != nil {
		_ = os.Remove(targetPath)
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	addr := strings.TrimSpace(r.Host)
	if addr == "" {
		addr = envOrDefault("ADDR", "127.0.0.1:2222")
	}

	scriptPath := filepath.Join(runtimeDir, "install-update.ps1")
	if err := writeUpdateScript(scriptPath); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	cmd := exec.Command("powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
		"-TargetExe", currentExe,
		"-NewExe", targetPath,
		"-AppDir", appDir,
		"-RuntimeDir", runtimeDir,
		"-Address", addr,
		"-OldPid", strconv.Itoa(os.Getpid()),
	)
	if err := cmd.Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("updater konnte nicht gestartet werden: %w", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"message":        "update wird installiert; der hub startet gleich neu",
		"latest_version": info.LatestVersion,
	})
}

func fetchLatestUpdateInfo(ctx context.Context) (updateInfo, error) {
	return fetchUpdateInfoFromManifest(ctx, updateManifestURL)
}

func fetchUpdateInfoFromManifest(ctx context.Context, endpoint string) (updateInfo, error) {
	manifest, err := fetchUpdateManifest(ctx, endpoint)
	if err != nil {
		return updateInfo{}, err
	}

	latest := strings.TrimSpace(manifest.LatestVersion)
	info := updateInfo{
		CurrentVersion: appVersion,
		LatestVersion:  latest,
		ReleaseURL:     manifest.ReleaseURL,
		AssetName:      manifest.AssetName,
		AssetURL:       manifest.AssetURL,
		ChecksumURL:    manifest.ChecksumURL,
		CanInstall:     runtime.GOOS == "windows",
	}
	if latest == "" {
		return info, fmt.Errorf("update-manifest enthaelt keine version")
	}
	info.UpdateAvailable = isNewerVersion(appVersion, latest)
	if !info.CanInstall {
		info.Message = "installation ist nur unter Windows moeglich"
	} else if info.AssetURL == "" || info.AssetName == "" || info.ChecksumURL == "" {
		info.CanInstall = false
		info.Message = "update-manifest enthaelt keine vollstaendigen windows-downloads"
	}
	return info, nil
}

func fetchUpdateManifest(ctx context.Context, endpoint string) (updateManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return updateManifest{}, err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return updateManifest{}, fmt.Errorf("update-manifest konnte nicht geladen werden: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return updateManifest{}, fmt.Errorf("update-manifest konnte nicht geladen werden: status %d", response.StatusCode)
	}
	var manifest updateManifest
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&manifest); err != nil {
		return updateManifest{}, fmt.Errorf("update-manifest ist ungueltig: %w", err)
	}
	if strings.TrimSpace(manifest.LatestVersion) == "" || strings.TrimSpace(manifest.AssetName) == "" || strings.TrimSpace(manifest.AssetURL) == "" || strings.TrimSpace(manifest.ChecksumURL) == "" {
		return updateManifest{}, fmt.Errorf("update-manifest ist unvollstaendig")
	}
	return manifest, nil
}

func fetchLatestGitHubRelease(ctx context.Context) (githubRelease, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	return fetchGitHubRelease(ctx, endpoint)
}

func fetchGitHubRelease(ctx context.Context, endpoint string) (githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("github konnte nicht erreicht werden: %w", err)
	}
	defer resp.Body.Close()
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return githubRelease{}, fmt.Errorf("github-api-limit erreicht; bitte spaeter erneut versuchen oder GITHUB_TOKEN setzen")
	}
	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("github-release-check fehlgeschlagen: status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("github-antwort konnte nicht gelesen werden: %w", err)
	}
	return release, nil
}

func findReleaseAsset(release githubRelease, tag string) githubReleaseAsset {
	expected := fmt.Sprintf("Lesezeichen-Hub_%s.exe", tag)
	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, expected) {
			return asset
		}
	}
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.HasPrefix(name, "lesezeichen-hub_") && strings.HasSuffix(name, ".exe") {
			return asset
		}
	}
	return githubReleaseAsset{}
}

func isNewerVersion(current, latest string) bool {
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" {
		return latest != "dev"
	}

	currentParts, currentOK := numericVersionParts(current)
	latestParts, latestOK := numericVersionParts(latest)
	if currentOK && latestOK {
		maxLen := len(currentParts)
		if len(latestParts) > maxLen {
			maxLen = len(latestParts)
		}
		for i := 0; i < maxLen; i++ {
			var c, l int
			if i < len(currentParts) {
				c = currentParts[i]
			}
			if i < len(latestParts) {
				l = latestParts[i]
			}
			if l > c {
				return true
			}
			if l < c {
				return false
			}
		}
		return false
	}

	return latest != current
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimPrefix(v, "v")
	return v
}

func numericVersionParts(v string) ([]int, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return nil, false
	}
	tokens := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	parts := make([]int, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		value, err := strconv.Atoi(token)
		if err != nil {
			return nil, false
		}
		parts = append(parts, value)
	}
	return parts, len(parts) > 0
}

func downloadFile(ctx context.Context, sourceURL, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download fehlgeschlagen: status %d", resp.StatusCode)
	}

	out, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("update-datei konnte nicht geschrieben werden: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("update-datei konnte nicht gespeichert werden: %w", err)
	}
	return nil
}

func verifyDownloadedAsset(ctx context.Context, checksumURL, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/"+appVersion)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("sha256-checksumme nicht erreichbar, ueberspringe pruefung: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		log.Printf("keine sha256-checksumme im release gefunden, ueberspringe pruefung")
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sha256-checksumme konnte nicht geladen werden: status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("sha256-checksumme konnte nicht gelesen werden: %w", err)
	}
	expected := firstSHA256Token(string(raw))
	if expected == "" {
		return fmt.Errorf("sha256-checksumme ist ungueltig")
	}

	file, err := os.Open(targetPath)
	if err != nil {
		return fmt.Errorf("update-datei konnte nicht geprueft werden: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("update-datei konnte nicht geprueft werden: %w", err)
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("sha256-pruefung fehlgeschlagen")
	}
	return nil
}

func firstSHA256Token(raw string) string {
	for _, token := range strings.Fields(raw) {
		token = strings.TrimSpace(token)
		if len(token) != 64 {
			continue
		}
		ok := true
		for _, r := range token {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				ok = false
				break
			}
		}
		if ok {
			return token
		}
	}
	return ""
}

func writeUpdateScript(scriptPath string) error {
	script := `param(
    [Parameter(Mandatory = $true)][string]$TargetExe,
    [Parameter(Mandatory = $true)][string]$NewExe,
    [Parameter(Mandatory = $true)][string]$AppDir,
    [Parameter(Mandatory = $true)][string]$RuntimeDir,
    [Parameter(Mandatory = $true)][string]$Address,
    [Parameter(Mandatory = $true)][int]$OldPid
)

$ErrorActionPreference = 'Stop'

$pidFile = Join-Path $RuntimeDir 'lesezeichen.pid'
$addrFile = Join-Path $RuntimeDir 'lesezeichen.addr'
$logFile = Join-Path $RuntimeDir 'lesezeichen.log'
$errLogFile = Join-Path $RuntimeDir 'lesezeichen.err.log'
$updateLogFile = Join-Path $RuntimeDir 'update.log'

function Write-UpdateLog {
    param([string]$Message)
    "$(Get-Date -Format s) $Message" | Add-Content -Path $updateLogFile -Encoding utf8
}

try {
    Write-UpdateLog "Update startet. Ziel: $TargetExe"
    Start-Sleep -Milliseconds 1500

    $proc = Get-Process -Id $OldPid -ErrorAction SilentlyContinue
    if ($null -ne $proc) {
        $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $OldPid" -ErrorAction SilentlyContinue
        $processPath = if ($null -ne $processInfo) { [string]$processInfo.ExecutablePath } else { '' }
        if ($processPath.Trim() -ieq $TargetExe.Trim()) {
            Stop-Process -Id $OldPid -Force -ErrorAction SilentlyContinue
        } else {
            Write-UpdateLog "PID $OldPid verweist nicht auf $TargetExe. Prozess wird nicht beendet."
        }
    }

    for ($i = 0; $i -lt 80; $i++) {
        $proc = Get-Process -Id $OldPid -ErrorAction SilentlyContinue
        if ($null -eq $proc) { break }
        Start-Sleep -Milliseconds 250
    }

    Copy-Item -LiteralPath $NewExe -Destination $TargetExe -Force
    Remove-Item -LiteralPath $NewExe -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue

    $env:ADDR = $Address
    $newProcess = Start-Process -FilePath $TargetExe -WorkingDirectory $AppDir -WindowStyle Hidden -PassThru -RedirectStandardOutput $logFile -RedirectStandardError $errLogFile
    $newProcess.Id | Set-Content -Path $pidFile -Encoding ascii
    $Address | Set-Content -Path $addrFile -Encoding ascii

    for ($i = 0; $i -lt 50; $i++) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "http://$Address/api/state" -TimeoutSec 1 | Out-Null
            Start-Process "http://$Address" | Out-Null
            Write-UpdateLog "Update abgeschlossen. Neue PID: $($newProcess.Id)"
            exit 0
        } catch {
            if ($newProcess.HasExited) { break }
            Start-Sleep -Milliseconds 200
        }
    }

    Write-UpdateLog "Update installiert, aber Neustart war nicht erreichbar."
    exit 1
} catch {
    Write-UpdateLog "Update fehlgeschlagen: $($_.Exception.Message)"
    exit 1
}
`
	return os.WriteFile(scriptPath, []byte(script), 0644)
}
