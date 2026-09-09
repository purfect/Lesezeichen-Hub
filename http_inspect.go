package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHTTPInspectRedirects = 10
	maxHTTPInspectRedirects     = 10
	maxHTTPInspectPreviewBytes  = 64 << 10
)

type httpInspectRequest struct {
	URL              string `json:"url"`
	Method           string `json:"method"`
	UseHEAD          bool   `json:"use_head"`
	FollowRedirects  *bool  `json:"follow_redirects"`
	MaxRedirects     int    `json:"max_redirects"`
	IncludeHeaders   bool   `json:"include_headers"`
	IncludeTLS       bool   `json:"include_tls"`
	BodyPreviewBytes int64  `json:"body_preview_bytes"`
}

type httpInspectResponse struct {
	API              string           `json:"api"`
	RequestedURL     string           `json:"requested_url"`
	FinalURL         string           `json:"final_url"`
	Redirected       bool             `json:"redirected"`
	Hops             []httpInspectHop `json:"hops"`
	LatencyMS        int64            `json:"latency_ms"`
	BodyPreview      string           `json:"body_preview,omitempty"`
	BodyPreviewBytes int64            `json:"body_preview_bytes,omitempty"`
	Error            string           `json:"error,omitempty"`
}

type httpInspectHop struct {
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Status       int               `json:"status"`
	StatusText   string            `json:"status_text"`
	Location     string            `json:"location,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	TLSExpiresAt string            `json:"tls_expires_at,omitempty"`
	TLS          *httpInspectTLS   `json:"tls,omitempty"`
}

type httpInspectTLS struct {
	Version            string   `json:"version"`
	CipherSuite        string   `json:"cipher_suite"`
	Subject            string   `json:"subject"`
	Issuer             string   `json:"issuer"`
	NotBefore          string   `json:"not_before"`
	NotAfter           string   `json:"not_after"`
	DaysRemaining      int      `json:"days_remaining"`
	SelfSigned         bool     `json:"self_signed"`
	ExpiringSoon       bool     `json:"expiring_soon"`
	Expired            bool     `json:"expired"`
	HostnameOK         bool     `json:"hostname_ok"`
	DNSNames           []string `json:"dns_names,omitempty"`
	SignatureAlgorithm string   `json:"signature_algorithm"`
	KeyAlgorithm       string   `json:"key_algorithm"`
	SerialNumber       string   `json:"serial_number"`
	ChainLength        int      `json:"chain_length"`
	WeakProtocol       bool     `json:"weak_protocol"`
}

func (app *application) handleHTTPInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var payload httpInspectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("ungueltiges JSON"))
		return
	}
	result, err := inspectHTTPURL(r.Context(), payload)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func inspectHTTPURL(ctx context.Context, input httpInspectRequest) (httpInspectResponse, error) {
	target, err := publicHTTPURL(input.URL)
	if err != nil {
		return httpInspectResponse{}, err
	}
	method, err := normalizeHTTPInspectMethod(input.Method, input.UseHEAD)
	if err != nil {
		return httpInspectResponse{}, err
	}
	maxRedirects := input.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultHTTPInspectRedirects
	}
	if maxRedirects > maxHTTPInspectRedirects {
		maxRedirects = maxHTTPInspectRedirects
	}
	previewBytes := input.BodyPreviewBytes
	if previewBytes < 0 {
		previewBytes = 0
	}
	if previewBytes > maxHTTPInspectPreviewBytes {
		previewBytes = maxHTTPInspectPreviewBytes
	}
	followRedirects := true
	if input.FollowRedirects != nil {
		followRedirects = *input.FollowRedirects
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
	result := httpInspectResponse{API: "http.inspect.v1", RequestedURL: target.String(), FinalURL: target.String()}
	for hopIndex := 0; hopIndex <= maxRedirects; hopIndex++ {
		hop, bodyPreview, bodyPreviewSize, nextURL, err := inspectHTTPHop(ctx, client, current, method, input.IncludeHeaders, input.IncludeTLS, previewBytes)
		if err != nil && method == http.MethodHead {
			method = http.MethodGet
			hop, bodyPreview, bodyPreviewSize, nextURL, err = inspectHTTPHop(ctx, client, current, method, input.IncludeHeaders, input.IncludeTLS, previewBytes)
		}
		if err != nil {
			result.Error = "Netzwerkfehler"
			result.FinalURL = current.String()
			result.LatencyMS = time.Since(started).Milliseconds()
			return result, nil
		}
		result.Hops = append(result.Hops, hop)
		result.FinalURL = hop.URL
		if bodyPreview != "" {
			result.BodyPreview = bodyPreview
			result.BodyPreviewBytes = bodyPreviewSize
		}
		if nextURL == nil || !followRedirects {
			break
		}
		if hopIndex == maxRedirects {
			result.Error = "Redirect-Limit erreicht"
			break
		}
		result.Redirected = true
		current = nextURL
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	return result, nil
}

func inspectHTTPHop(ctx context.Context, client *http.Client, target *url.URL, method string, includeHeaders, includeTLS bool, bodyPreviewBytes int64) (httpInspectHop, string, int64, *url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return httpInspectHop{}, "", 0, nil, err
	}
	request.Header.Set("User-Agent", "Lesezeichen-Hub-HTTP-Inspect/1.0")
	response, err := client.Do(request)
	if err != nil {
		return httpInspectHop{}, "", 0, nil, err
	}
	defer response.Body.Close()

	hop := httpInspectHop{
		URL:        target.String(),
		Method:     method,
		Status:     response.StatusCode,
		StatusText: response.Status,
	}
	if includeHeaders {
		hop.Headers = flattenHeaders(response.Header)
	}
	if includeTLS && response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		hop.TLSExpiresAt = response.TLS.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339)
		hop.TLS = buildTLSInfo(response.TLS, target.Hostname())
	}

	preview := ""
	previewSize := int64(0)
	if bodyPreviewBytes > 0 && method == http.MethodGet {
		body, _ := io.ReadAll(io.LimitReader(response.Body, bodyPreviewBytes))
		preview = string(body)
		previewSize = int64(len(body))
	} else {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
	}

	location := strings.TrimSpace(response.Header.Get("Location"))
	if location == "" || response.StatusCode < 300 || response.StatusCode > 399 {
		return hop, preview, previewSize, nil, nil
	}
	hop.Location = location
	nextURL, err := target.Parse(location)
	if err != nil {
		return hop, preview, previewSize, nil, nil
	}
	if _, err := publicHTTPURL(nextURL.String()); err != nil {
		return hop, preview, previewSize, nil, nil
	}
	return hop, preview, previewSize, nextURL, nil
}

func normalizeHTTPInspectMethod(method string, useHEAD bool) (string, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		if useHEAD {
			return http.MethodHead, nil
		}
		return http.MethodGet, nil
	}
	if method != http.MethodHead && method != http.MethodGet {
		return "", fmt.Errorf("nur GET und HEAD sind erlaubt")
	}
	return method, nil
}

func flattenHeaders(headers http.Header) map[string]string {
	flattened := make(map[string]string, len(headers))
	for key, values := range headers {
		flattened[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	return flattened
}

func buildTLSInfo(state *tls.ConnectionState, hostname string) *httpInspectTLS {
	cert := state.PeerCertificates[0]
	now := time.Now().UTC()
	daysRemaining := int(cert.NotAfter.UTC().Sub(now).Hours() / 24)
	selfSigned := len(state.PeerCertificates) == 1 && cert.CheckSignatureFrom(cert) == nil

	hostnameOK := true
	if hostname != "" {
		hostnameOK = cert.VerifyHostname(hostname) == nil
	}

	weakProtocol := state.Version < tls.VersionTLS12

	info := &httpInspectTLS{
		Version:            tls.VersionName(state.Version),
		CipherSuite:        tls.CipherSuiteName(state.CipherSuite),
		Subject:            cert.Subject.CommonName,
		Issuer:             cert.Issuer.CommonName,
		NotBefore:          cert.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:           cert.NotAfter.UTC().Format(time.RFC3339),
		DaysRemaining:      daysRemaining,
		SelfSigned:         selfSigned,
		ExpiringSoon:       daysRemaining >= 0 && daysRemaining <= 21,
		Expired:            daysRemaining < 0,
		HostnameOK:         hostnameOK,
		DNSNames:           cert.DNSNames,
		SignatureAlgorithm: cert.SignatureAlgorithm.String(),
		KeyAlgorithm:       cert.PublicKeyAlgorithm.String(),
		SerialNumber:       cert.SerialNumber.String(),
		ChainLength:        len(state.PeerCertificates),
		WeakProtocol:       weakProtocol,
	}
	return info
}
