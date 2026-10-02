package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/fx"
	"vpsbill/internal/settings"
)

func (a adminSettings) locale(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.settings.Current().Locale})
}

func (a adminSettings) updateLocale(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DefaultLang string  `json:"default_lang"`
		USDEnabled  bool    `json:"usd_enabled"`
		USDRate     float64 `json:"usd_rate"`
		USDRateAuto bool    `json:"usd_rate_auto"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	a.localeResult(w, a.settings.SetLocale(r.Context(), settings.LocaleSettings{
		DefaultLang: input.DefaultLang, USDEnabled: input.USDEnabled, USDRate: input.USDRate, USDRateAuto: input.USDRateAuto,
	}, principalFromContext(r.Context()).UserID))
}

// fetchRate reads the market rate now and stores it, so staff need not
// wait for the next automatic update.
func (a adminSettings) fetchRate(w http.ResponseWriter, r *http.Request) {
	rate, err := fx.Fetch(r.Context(), &http.Client{Timeout: 20 * time.Second})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "rate_unavailable", "message": "获取汇率失败：" + err.Error()})
		return
	}
	current := a.settings.Current().Locale
	current.USDRate = rate
	a.localeResult(w, a.settings.SetLocale(r.Context(), current, principalFromContext(r.Context()).UserID))
}

func (a adminSettings) localeResult(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrInvalidSettings):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "settings_update_failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": a.settings.Current().Locale})
	}
}

// customerSetLocale remembers the language a customer reads the site in,
// which is the language of the mail they get.
func (a *authenticator) customerSetLocale(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Locale string `json:"locale"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Locale != "zh" && input.Locale != "en" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "语言只能是 zh 或 en"})
		return
	}
	if err := a.store.SetLocale(r.Context(), customerPrincipalFromContext(r.Context()).UserID, input.Locale); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
