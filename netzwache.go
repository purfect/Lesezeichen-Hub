package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func (app *application) handleNetzWacheCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	target, err := publicHTTPURL(r.URL.Query().Get("url"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	started := time.Now()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	request.Header.Set("User-Agent", "Lesezeichen-Hub-NetzWache/1.0")
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy:       http.ProxyFromEnvironment,
			DialContext: publicDialContext,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "latency": latency, "message": "Netzwerkfehler"})
		return
	}
	defer response.Body.Close()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest,
		"latency": latency,
		"message": fmt.Sprintf("HTTP %d", response.StatusCode),
	})
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
