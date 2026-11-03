package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const maxImportedNoteBytes = 5 << 20

type importedNoteText struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	SourceURL string `json:"source_url"`
}

func (app *application) handleNoteURLImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	if len(payload.URL) > 4096 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("URL ist zu lang"))
		return
	}
	target, err := normalizeNoteImportURL(payload.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	transport := &http.Transport{
		DialContext:           publicNoteDialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   25 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("zu viele Weiterleitungen")
			}
			_, err := normalizeNoteImportURL(req.URL.String())
			return err
		},
	}
	document, status, err := downloadNoteText(r.Context(), client, target)
	if err != nil {
		writeErr(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func normalizeNoteImportURL(raw string) (*url.URL, error) {
	target, err := publicHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	if target.User != nil {
		return nil, fmt.Errorf("URLs mit Zugangsdaten sind nicht erlaubt")
	}
	if strings.EqualFold(target.Hostname(), "github.com") {
		segments := strings.Split(strings.Trim(target.Path, "/"), "/")
		if len(segments) >= 5 && (segments[2] == "blob" || segments[2] == "raw") {
			target.Scheme = "https"
			target.Host = "raw.githubusercontent.com"
			target.Path = "/" + strings.Join(append(segments[:2], segments[3:]...), "/")
			target.RawPath = ""
			target.RawQuery = ""
			target.Fragment = ""
		}
	}
	return target, nil
}

// Resolve and connect to the checked address itself, so a later DNS answer
// cannot switch the request to a private address.
func publicNoteDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("keine Adresse fuer den Host gefunden")
	}
	for _, ip := range addresses {
		if !isPublicIP(ip.Unmap()) {
			return nil, fmt.Errorf("lokale und private Netzadressen sind nicht erlaubt")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func downloadNoteText(ctx context.Context, client *http.Client, target *url.URL) (importedNoteText, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return importedNoteText{}, http.StatusBadRequest, fmt.Errorf("URL ist ungueltig")
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub-Notizimport/1.0")
	req.Header.Set("Accept", "text/plain, text/markdown, application/json, */*")
	response, err := client.Do(req)
	if err != nil {
		return importedNoteText{}, http.StatusBadGateway, fmt.Errorf("Datei konnte nicht heruntergeladen werden: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return importedNoteText{}, http.StatusBadGateway, fmt.Errorf("Quelle antwortet mit HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImportedNoteBytes+1))
	if err != nil {
		return importedNoteText{}, http.StatusBadGateway, fmt.Errorf("Datei konnte nicht gelesen werden")
	}
	if len(data) > maxImportedNoteBytes {
		return importedNoteText{}, http.StatusRequestEntityTooLarge, fmt.Errorf("Textdatei ist groesser als 5 MB")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return importedNoteText{}, http.StatusUnprocessableEntity, fmt.Errorf("Quelle ist keine UTF-8-Textdatei")
	}
	title := strings.TrimSpace(path.Base(response.Request.URL.Path))
	if title == "" || title == "." || title == "/" {
		title = "Importierte Notiz"
	} else if extension := path.Ext(title); extension != title {
		title = strings.TrimSuffix(title, extension)
	}
	if len([]rune(title)) > 200 {
		title = string([]rune(title)[:200])
	}
	return importedNoteText{Title: title, Content: string(data), SourceURL: response.Request.URL.String()}, http.StatusOK, nil
}
