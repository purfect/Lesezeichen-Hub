package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxRedirectInspectorHops = 10

type redirectInspectorRequest struct {
	URL     string `json:"url"`
	UseHEAD bool   `json:"use_head"`
}

type redirectInspectorResponse struct {
	RequestedURL string                 `json:"requested_url"`
	FinalURL     string                 `json:"final_url"`
	Redirected   bool                   `json:"redirected"`
	Hops         []redirectInspectorHop `json:"hops"`
	LatencyMS    int64                  `json:"latency_ms"`
	Error        string                 `json:"error,omitempty"`
}

type redirectInspectorHop struct {
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Status       int               `json:"status"`
	StatusText   string            `json:"status_text"`
	Location     string            `json:"location,omitempty"`
	Headers      map[string]string `json:"headers"`
	TLSExpiresAt string            `json:"tls_expires_at,omitempty"`
}

func (app *application) handleRedirectInspector(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var payload redirectInspectorRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	result, err := inspectRedirects(r.Context(), payload.URL, payload.UseHEAD)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func inspectRedirects(ctx context.Context, rawURL string, useHEAD bool) (redirectInspectorResponse, error) {
	target, err := publicHTTPURL(rawURL)
	if err != nil {
		return redirectInspectorResponse{}, err
	}
	started := time.Now()
	client := &http.Client{
		Timeout:   25 * time.Second,
		Transport: &http.Transport{DialContext: publicDialContext},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	current := target
	method := http.MethodGet
	if useHEAD {
		method = http.MethodHead
	}
	response := redirectInspectorResponse{RequestedURL: target.String(), FinalURL: target.String()}
	for hopIndex := 0; hopIndex <= maxRedirectInspectorHops; hopIndex++ {
		hop, nextURL, err := inspectRedirectHop(ctx, client, current, method)
		if err != nil && method == http.MethodHead {
			method = http.MethodGet
			hop, nextURL, err = inspectRedirectHop(ctx, client, current, method)
		}
		if err != nil {
			response.Error = "Netzwerkfehler"
			response.FinalURL = current.String()
			response.LatencyMS = time.Since(started).Milliseconds()
			return response, nil
		}
		response.Hops = append(response.Hops, hop)
		response.FinalURL = hop.URL
		if nextURL == nil {
			break
		}
		if hopIndex == maxRedirectInspectorHops {
			response.Error = "Redirect-Limit erreicht"
			break
		}
		response.Redirected = true
		current = nextURL
	}
	response.LatencyMS = time.Since(started).Milliseconds()
	return response, nil
}

func inspectRedirectHop(ctx context.Context, client *http.Client, target *url.URL, method string) (redirectInspectorHop, *url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return redirectInspectorHop{}, nil, err
	}
	request.Header.Set("User-Agent", "Lesezeichen-Hub-Redirect-Inspector/1.0")
	response, err := client.Do(request)
	if err != nil {
		return redirectInspectorHop{}, nil, err
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 4096)

	hop := redirectInspectorHop{
		URL:        target.String(),
		Method:     method,
		Status:     response.StatusCode,
		StatusText: response.Status,
		Headers:    flattenHeaders(response.Header),
	}
	if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		hop.TLSExpiresAt = response.TLS.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339)
	}
	location := strings.TrimSpace(response.Header.Get("Location"))
	if location == "" || response.StatusCode < 300 || response.StatusCode > 399 {
		return hop, nil, nil
	}
	hop.Location = location
	nextURL, err := target.Parse(location)
	if err != nil {
		return hop, nil, nil
	}
	if _, err := publicHTTPURL(nextURL.String()); err != nil {
		return hop, nil, nil
	}
	return hop, nextURL, nil
}

func flattenHeaders(headers http.Header) map[string]string {
	flattened := make(map[string]string, len(headers))
	for key, values := range headers {
		flattened[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	return flattened
}
