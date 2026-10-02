package app

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/fx"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

func (a adminSettings) locale(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.settings.Current().Locale})
}

func (a adminSettings) updateLocale(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DefaultLang     string  `json:"default_lang"`
		CNYHidden       bool    `json:"cny_hidden"`
		USDEnabled      bool    `json:"usd_enabled"`
		DefaultCurrency string  `json:"default_currency"`
		USDRate         float64 `json:"usd_rate"`
		USDRateAuto     bool    `json:"usd_rate_auto"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	a.localeResult(w, a.settings.SetLocale(r.Context(), settings.LocaleSettings{
		DefaultLang: input.DefaultLang, CNYHidden: input.CNYHidden, USDEnabled: input.USDEnabled, DefaultCurrency: input.DefaultCurrency,
		USDRate: input.USDRate, USDRateAuto: input.USDRateAuto,
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

// mailLang is the language to write to a user in: the one stored for the
// user, else the one the page asking for the mail is in.
func (a *authenticator) mailLang(r *http.Request, locale string) string {
	if locale == "" {
		locale = r.Header.Get("Accept-Language")
	}
	return a.settings.Current().Lang(locale)
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

// ledger says what the books are kept in and what a switch would convert.
func (a adminSettings) ledger(w http.ResponseWriter, r *http.Request) {
	state, err := a.settings.LedgerState(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	locale := a.settings.Current().Locale
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ledger_currency": locale.Ledger(), "usd_rate": locale.USDRate, "state": state}})
}

// switchLedger re-denominates the books in the other currency at the
// current rate. The caller names the currency it expects to leave, so a
// repeated request cannot switch back.
func (a adminSettings) switchLedger(w http.ResponseWriter, r *http.Request) {
	var input struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if current := a.settings.Current().Locale.Ledger(); input.From != current {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ledger_changed", "message": "记账币种已经变化，请刷新页面后再操作"})
		return
	}
	counts, err := a.settings.SwitchLedger(r.Context(), input.To, principalFromContext(r.Context()).UserID)
	switch {
	case errors.Is(err, postgres.ErrLedgerBusy):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ledger_busy", "message": "有在线支付正在进行，暂时不能切换记账币种。请先停用支付网关，等 30 分钟后未完成的支付过期再切换"})
	case errors.Is(err, settings.ErrInvalidSettings):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")})
	case err != nil:
		slog.Default().Error("switch ledger currency", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "ledger_switch_failed", "message": "切换失败，账本没有任何改动"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ledger_currency": input.To, "counts": counts, "locale": a.settings.Current().Locale}})
	}
}
