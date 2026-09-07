package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	_ "modernc.org/sqlite"
)

func (app *application) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	groups, err := app.fetchState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (app *application) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"external_prices":      app.externalPrices,
			"metal_prices_enabled": app.metalPricesEnabled(),
			"version":              appVersion,
		})
	case http.MethodPut:
		var input struct {
			MetalPricesEnabled *bool `json:"metal_prices_enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil || input.MetalPricesEnabled == nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("metal_prices_enabled muss angegeben werden"))
			return
		}
		value := "false"
		if *input.MetalPricesEnabled {
			value = "true"
		}
		if _, err := app.db.ExecContext(r.Context(), `INSERT INTO app_settings(key, value) VALUES('metal_prices_enabled', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, value); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"metal_prices_enabled": *input.MetalPricesEnabled})
	default:
		methodNotAllowed(w)
	}
}

func (app *application) metalPricesEnabled() bool {
	return app.externalPrices && readAppSetting(app.db, "metal_prices_enabled") != "false"
}
