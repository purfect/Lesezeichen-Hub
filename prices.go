package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
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
	htmlSource = strings.ReplaceAll(htmlSource, "\n", " ")
	htmlSource = strings.ReplaceAll(htmlSource, "\r", " ")

	products := make([]silverProduct, 0)
	seen := make(map[string]struct{})

	productBlocksRe := regexp.MustCompile(`(?is)<product-item[^>]*>(.*?)</product-item>`)
	blocks := productBlocksRe.FindAllStringSubmatch(htmlSource, -1)
	if len(blocks) > 0 {
		for _, block := range blocks {
			blockHTML := block[1]
			anchorRe := regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
			anchorMatches := anchorRe.FindAllStringSubmatchIndex(blockHTML, -1)
			if len(anchorMatches) == 0 {
				continue
			}

			var name string
			var href string
			for _, match := range anchorMatches {
				candidateHref := blockHTML[match[2]:match[3]]
				candidateContent := blockHTML[match[4]:match[5]]
				candidateName := sanitizeHTMLText(candidateContent)
				if candidateName == "" {
					continue
				}
				name = candidateName
				href = candidateHref
				break
			}
			if name == "" || href == "" {
				continue
			}

			priceText, price, ok := findNearbyPrice(blockHTML, 0, len(blockHTML))
			if !ok || price <= 0 {
				continue
			}

			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			products = append(products, silverProduct{
				Name:      cleanProductName(name),
				Category:  "Silber 1oz",
				Price:     price,
				PriceText: priceText,
				URL:       normalizeProductURL(href),
			})
		}
	}

	if len(products) == 0 {
		anchorRe := regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
		anchorMatches := anchorRe.FindAllStringSubmatchIndex(htmlSource, -1)
		if len(anchorMatches) == 0 {
			return nil, fmt.Errorf("keine produkt-links gefunden in %s", source)
		}
		for _, match := range anchorMatches {
			anchorHTML := htmlSource[match[0]:match[1]]
			href := htmlSource[match[2]:match[3]]
			content := htmlSource[match[4]:match[5]]
			name := sanitizeHTMLText(content)
			if name == "" || !looksLike1ozSilverProduct(name, href, anchorHTML) {
				continue
			}
			priceText, price, ok := findNearbyPrice(htmlSource, match[0], match[1])
			if !ok || price <= 0 {
				continue
			}
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			products = append(products, silverProduct{
				Name:      cleanProductName(name),
				Category:  "Silber 1oz",
				Price:     price,
				PriceText: priceText,
				URL:       normalizeProductURL(href),
			})
		}
	}

	if len(products) == 0 {
		return nil, fmt.Errorf("keine 1oz-produkte gefunden in %s", source)
	}

	return products, nil
}

func looksLike1ozSilverProduct(name, href, anchorHTML string) bool {
	text := strings.ToLower(strings.TrimSpace(name + " " + href + " " + anchorHTML))
	if strings.Contains(text, "/anlegen/silber") || strings.Contains(text, "?fine_weight") || strings.Contains(text, "sort=") || strings.Contains(text, "price%5b") || strings.Contains(text, "/anlegen/silber/") {
		return false
	}
	if strings.Contains(text, "silbermünze") || strings.Contains(text, "silbermuenze") {
		return true
	}
	if strings.Contains(text, "silber") && strings.Contains(text, "1") && strings.Contains(text, "oz") {
		return true
	}
	if strings.Contains(text, "1 oz") || strings.Contains(text, "1oz") || strings.Contains(text, "31,1") || strings.Contains(text, "31,1g") || strings.Contains(text, "31,1 g") {
		return true
	}
	return false
}

func findNearbyPrice(htmlSource string, anchorStart, anchorEnd int) (string, float64, bool) {
	start := anchorStart
	if start < 0 {
		start = 0
	}
	end := anchorEnd + 20000
	if end > len(htmlSource) {
		end = len(htmlSource)
	}
	context := htmlSource[start:end]

	pricePatterns := []string{
		`(?is)<span[^>]*itemprop=["']price["'][^>]*content=["']([0-9.]+)["'][^>]*>`,
		`(?is)<span[^>]*content=["']([0-9.]+)["'][^>]*itemprop=["']price["'][^>]*>`,
		`(?is)<span[^>]*class=["'][^"']*money-price__amount[^"']*["'][^>]*>([^<]+)</span>`,
		`(?is)<span[^>]*itemprop=["']price["'][^>]*class=["'][^"']*money-price__amount[^"']*["'][^>]*>([^<]+)</span>`,
		`(?is)([0-9]{1,3}(?:[\.,][0-9]{3})*(?:[\.,][0-9]{2})?)\s*€`,
	}

	for _, pattern := range pricePatterns {
		priceRe := regexp.MustCompile(pattern)
		match := priceRe.FindStringSubmatch(context)
		if len(match) < 2 {
			continue
		}
		price, err := parsePriceString(match[1])
		if err == nil && price > 0 {
			return strings.TrimSpace(match[1]), price, true
		}
	}

	return "", 0, false
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

func normalizeProductURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return "https://www.edelmetall-handel.de" + raw
	}
	return "https://www.edelmetall-handel.de/" + raw
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
