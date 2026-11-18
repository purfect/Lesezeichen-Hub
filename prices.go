package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (app *application) handleMetalPrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !app.metalPricesEnabled() {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("externe preisabfragen sind deaktiviert"))
		return
	}

	prices, err := app.getMetalPrices(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	app.saveSilverPriceSnapshot(r.Context(), prices)

	writeJSON(w, http.StatusOK, prices)
}

func (app *application) handleSilverPrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !app.metalPricesEnabled() {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("externe preisabfragen sind deaktiviert"))
		return
	}

	forceRefresh := r.URL.Query().Get("refresh") == "1"
	prices, err := app.getSilverPrices(r.Context(), forceRefresh)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, prices)
}

func (app *application) saveSilverPriceSnapshot(ctx context.Context, metal metalPricesPayload) {
	if metal.SilverEURPerGram <= 0 {
		return
	}

	silver, err := app.getSilverPrices(ctx, false)
	if err != nil || silver.BestProduct == nil || silver.BestProduct.Price <= 0 {
		return
	}

	fetchedAt := metal.FetchedAt
	if silver.FetchedAt.After(fetchedAt) {
		fetchedAt = silver.FetchedAt
	}
	_, err = app.db.ExecContext(ctx, `INSERT OR IGNORE INTO silver_price_history
		(fetched_at, eur_per_g, best_eur_per_ounce, best_product_name, best_product_url)
		VALUES (?, ?, ?, ?, ?)`, fetchedAt, metal.SilverEURPerGram, silver.BestProduct.Price, silver.BestProduct.Name, silver.BestProduct.URL)
	if err != nil {
		log.Printf("save silver price snapshot: %v", err)
	}
}

func (app *application) handleSilverPriceHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	from, err := parseHistoryDate(r.URL.Query().Get("from"), false)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	to, err := parseHistoryDate(r.URL.Query().Get("to"), true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("startdatum darf nicht nach dem enddatum liegen"))
		return
	}

	query := `SELECT fetched_at, eur_per_g, best_eur_per_ounce, best_product_name, best_product_url
		FROM silver_price_history WHERE (? = '' OR substr(fetched_at, 1, 10) >= ?) AND (? = '' OR substr(fetched_at, 1, 10) <= ?) ORDER BY fetched_at ASC`
	rows, err := app.db.QueryContext(r.Context(), query, historyDateValue(from), historyDateValue(from), historyDateValue(to), historyDateValue(to))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	entries := make([]silverPriceHistoryEntry, 0)
	for rows.Next() {
		var entry silverPriceHistoryEntry
		if err := rows.Scan(&entry.FetchedAt, &entry.EURPerGram, &entry.BestEURPerOunce, &entry.BestProductName, &entry.BestProductURL); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, entries)
}

func (app *application) handleSilverPriceHistoryBounds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	var bounds silverPriceHistoryBounds
	if err := app.db.QueryRowContext(r.Context(), `SELECT COALESCE(MIN(fetched_at), ''), COALESCE(MAX(fetched_at), '') FROM silver_price_history`).Scan(&bounds.Earliest, &bounds.Latest); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, bounds)
}

func parseHistoryDate(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("datum muss das Format JJJJ-MM-TT haben")
	}
	if endOfDay {
		return value.AddDate(0, 0, 1).Add(-time.Nanosecond), nil
	}
	return value, nil
}

func historyDateValue(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02")
}

func (app *application) getSilverPrices(ctx context.Context, forceRefresh bool) (silverPricesPayload, error) {
	const cacheTTL = 2 * time.Hour

	app.silverPricesMu.RLock()
	if !forceRefresh && !app.silverPricesAt.IsZero() && time.Since(app.silverPricesAt) < cacheTTL {
		cached := app.silverPrices
		app.silverPricesMu.RUnlock()
		return cached, nil
	}
	app.silverPricesMu.RUnlock()

	fresh, err := fetchSilverPrices(ctx)
	if err != nil {
		return silverPricesPayload{}, err
	}
	if fresh.BestProduct == nil || fresh.BestProduct.Price <= 0 {
		app.silverPricesMu.RLock()
		if !app.silverPricesAt.IsZero() {
			cached := app.silverPrices
			app.silverPricesMu.RUnlock()
			return cached, nil
		}
		app.silverPricesMu.RUnlock()
		return fresh, nil
	}

	app.silverPricesMu.Lock()
	app.silverPrices = fresh
	app.silverPricesAt = fresh.FetchedAt
	app.silverPricesMu.Unlock()

	return fresh, nil
}

func (app *application) handleSilverPricesPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	page, err := fs.ReadFile(app.webFS, "silver-prices.html")
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("seite nicht gefunden"))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(page); err != nil {
		log.Printf("write silver-prices.html: %v", err)
	}
}

func (app *application) handleSilverPriceHistoryPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	page, err := fs.ReadFile(app.webFS, "silver-price-history.html")
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("seite nicht gefunden"))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(page); err != nil {
		log.Printf("write silver-price-history.html: %v", err)
	}
}

func (app *application) getMetalPrices(ctx context.Context) (metalPricesPayload, error) {
	const cacheTTL = 2 * time.Hour

	app.metalPricesMu.RLock()
	hasCache := !app.metalPricesAt.IsZero()
	cacheFresh := hasCache && time.Since(app.metalPricesAt) < cacheTTL
	if cacheFresh {
		cached := app.metalPrices
		cached.Cached = true
		cached.Stale = false
		app.metalPricesMu.RUnlock()
		return cached, nil
	}
	app.metalPricesMu.RUnlock()

	gold, silver, fetchErr := fetchBankPricesFromHomepage(ctx)

	now := time.Now().UTC()
	if fetchErr == nil {
		fresh := metalPricesPayload{
			GoldEURPerGram:   gold,
			SilverEURPerGram: silver,
			FetchedAt:        now,
			Cached:           false,
			Stale:            false,
		}

		app.metalPricesMu.Lock()
		app.metalPrices = fresh
		app.metalPricesAt = now
		app.metalPricesErr = ""
		app.metalPricesMu.Unlock()

		return fresh, nil
	}

	app.metalPricesMu.RLock()
	if !app.metalPricesAt.IsZero() {
		stale := app.metalPrices
		stale.Cached = true
		stale.Stale = true
		stale.LastError = fetchErr.Error()
		app.metalPricesMu.RUnlock()
		return stale, nil
	}
	app.metalPricesMu.RUnlock()

	return metalPricesPayload{}, fmt.Errorf("preise konnten nicht geladen werden: %s", fetchErr.Error())
}

func fetchSilverPrices(ctx context.Context) (silverPricesPayload, error) {
	const sourceURL = "https://www.edelmetall-handel.de/anlegen/silber?fine_weight%5B%5D=31%2C1+g+%281oz%29&ipp=100&sort=price_asc"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return silverPricesPayload{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return silverPricesPayload{}, fmt.Errorf("request fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return silverPricesPayload{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024))
	if err != nil {
		return silverPricesPayload{}, fmt.Errorf("response konnte nicht gelesen werden: %w", err)
	}

	products, err := parseSilverProductsFromHTML(string(body), sourceURL)
	if err != nil {
		return silverPricesPayload{
			BestProduct: &silverProduct{
				Name:      "Aktuelle Preisabfrage momentan nicht verfügbar",
				Category:  "Silber 1oz",
				Price:     0,
				PriceText: "n/a",
				URL:       sourceURL,
			},
			AllProducts: []silverProduct{},
			FetchedAt:   time.Now().UTC(),
			Source:      sourceURL,
		}, nil
	}

	sort.Slice(products, func(i, j int) bool {
		return products[i].Price < products[j].Price
	})

	limit := len(products)
	if limit > 20 {
		limit = 20
	}

	var bestProduct *silverProduct
	if len(products) > 0 {
		best := products[0]
		bestProduct = &best
	}

	return silverPricesPayload{
		BestProduct: bestProduct,
		AllProducts: products[:limit],
		FetchedAt:   time.Now().UTC(),
		Source:      sourceURL,
	}, nil
}

func parseSilverProductsFromHTML(htmlSource, source string) ([]silverProduct, error) {
	products := make([]silverProduct, 0)
	seen := make(map[string]struct{})
	blocks := regexp.MustCompile(`(?is)<product-item\b[^>]*>(.*?)</product-item>`).FindAllStringSubmatch(htmlSource, -1)
	anchors := regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	names := regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\bproduct-item__name\b[^"']*["'][^>]*>(.*?)</span>`)
	prices := regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\bjs-product-item__default-price\b[^"']*["'][^>]*>(.*?)</span>`)
	for _, block := range blocks {
		var name, productURL string
		for _, anchor := range anchors.FindAllStringSubmatch(block[1], -1) {
			match := names.FindStringSubmatch(anchor[2])
			if len(match) == 0 {
				continue
			}
			name = cleanProductName(sanitizeHTMLText(match[1]))
			productURL = normalizeProductURL(anchor[1], source)
			break
		}
		if name == "" || productURL == "" || !looksLike1ozSilverProduct(name) {
			continue
		}
		priceMatch := prices.FindStringSubmatch(block[1])
		if len(priceMatch) == 0 {
			continue
		}
		priceText := sanitizeHTMLText(priceMatch[1])
		price, err := parsePriceString(priceText)
		if err != nil || price <= 0 {
			continue
		}
		if _, exists := seen[productURL]; exists {
			continue
		}
		seen[productURL] = struct{}{}
		products = append(products, silverProduct{
			Name: name, Category: "Silber 1oz", Price: price,
			PriceText: priceText, URL: productURL,
		})
	}

	if len(products) == 0 {
		return nil, fmt.Errorf("keine 1oz-produkte gefunden in %s", source)
	}

	return products, nil
}

func looksLike1ozSilverProduct(name string) bool {
	name = strings.ToLower(name)
	// The shop lists JUNK-Silbermünze cards whose detail pages return 404.
	if strings.HasPrefix(name, "junk-silbermünze") || strings.HasPrefix(name, "junk-silbermuenze") {
		return false
	}
	return strings.Contains(name, "silber") &&
		regexp.MustCompile(`\b(?:1\s*oz|31[,.]1\s*g)\b`).MatchString(name) &&
		!regexp.MustCompile(`\b\d+\s*(?:x|×)\s*1\s*oz\b`).MatchString(name)
}

func cleanProductName(name string) string {
	normalized := strings.TrimSpace(name)
	normalized = strings.ReplaceAll(normalized, "- Hauptansicht", "")
	normalized = strings.ReplaceAll(normalized, "Hauptansicht", "")
	normalized = strings.TrimSpace(normalized)
	return normalized
}

func parsePriceString(raw string) (float64, error) {
	normalized := strings.TrimSpace(raw)
	normalized = strings.ReplaceAll(normalized, "€", "")
	normalized = strings.ReplaceAll(normalized, "\u00a0", "")
	if strings.Contains(normalized, ",") {
		normalized = strings.ReplaceAll(normalized, ".", "")
		normalized = strings.ReplaceAll(normalized, ",", ".")
	}
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return 0, fmt.Errorf("leerer preis")
	}
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func normalizeProductURL(raw, source string) string {
	base, err := url.Parse(source)
	if err != nil {
		return ""
	}
	link, err := url.Parse(html.UnescapeString(strings.TrimSpace(raw)))
	if err != nil || link.Path == "" {
		return ""
	}
	resolved := base.ResolveReference(link)
	if resolved.Host != base.Host || (resolved.Scheme != "https" && resolved.Scheme != "http") ||
		strings.HasPrefix(resolved.Path, "/anlegen") {
		return ""
	}
	resolved.Fragment = ""
	return resolved.String()
}

func sanitizeHTMLText(raw string) string {
	text := strings.ReplaceAll(raw, "&nbsp;", " ")
	text = html.UnescapeString(text)
	text = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

func joinErrors(errs ...error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

func fetchBankPricesFromHomepage(ctx context.Context) (float64, float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.scheideanstalt.de/nc?header-prices-v2", nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "Lesezeichen-Hub/1.0 (+http://localhost)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("request fehlgeschlagen")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 3*1024*1024))
	if err != nil {
		return 0, 0, fmt.Errorf("response konnte nicht gelesen werden")
	}

	html := string(body)
	if !strings.Contains(strings.ToLower(html), "bankpreis") {
		return 0, 0, fmt.Errorf("header-prices endpoint ohne bankpreis-daten")
	}

	gold, err := parseBankPriceEURPerGram(html, "Gold")
	if err != nil {
		return 0, 0, err
	}
	silver, err := parseBankPriceEURPerGram(html, "Silber")
	if err != nil {
		return 0, 0, err
	}

	return gold, silver, nil
}

func parseBankPriceEURPerGram(html, metalLabel string) (float64, error) {
	re := regexp.MustCompile(`(?is)` + regexp.QuoteMeta(metalLabel) + `\s*\(Bankpreis\)\s*</td>\s*<td[^>]*>\s*<span[^>]*>\s*([0-9]{1,3}(?:\.[0-9]{3})*(?:,[0-9]{2})?)\s*</span>\s*€\s*/\s*g\s*</td>\s*<td[^>]*>\s*<span[^>]*>\s*([0-9]{1,3}(?:\.[0-9]{3})*(?:,[0-9]{2})?)\s*</span>\s*€\s*/\s*g`)
	match := re.FindStringSubmatch(html)
	if len(match) != 3 {
		return 0, fmt.Errorf("%s: bankpreis nicht gefunden", strings.ToLower(metalLabel))
	}

	postPrice, err := parseGermanDecimal(match[1])
	if err != nil {
		return 0, fmt.Errorf("%s: postankaufpreis ungueltig", strings.ToLower(metalLabel))
	}
	switchPrice, err := parseGermanDecimal(match[2])
	if err != nil {
		return 0, fmt.Errorf("%s: schalterankaufpreis ungueltig", strings.ToLower(metalLabel))
	}

	if switchPrice > 0 {
		return switchPrice, nil
	}

	return postPrice, nil
}

func parseGermanDecimal(raw string) (float64, error) {
	normalized := strings.TrimSpace(raw)
	normalized = strings.ReplaceAll(normalized, ".", "")
	normalized = strings.ReplaceAll(normalized, ",", ".")
	return strconv.ParseFloat(normalized, 64)
}
