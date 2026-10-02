package app

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
)

// siteMeta is what every page needs about the site before it renders.
func siteMeta(cfg config.Config, runtime *settings.Manager, r *http.Request) map[string]any {
	current := runtime.Current()
	return map[string]any{
		"surface":                  surfaceOf(r),
		"admin_url":                current.AdminURL,
		"public_url":               current.PublicURL,
		"name":                     current.AppName,
		"logo_url":                 current.Logo(),
		"logo_mode":                current.LogoMode,
		"logo_dark_url":            current.LogoDark(),
		"favicon_url":              current.Favicon(),
		"environment":              cfg.Environment,
		"installed":                current.Installed,
		"password_reset_mail":      current.SMTP.Configured(),
		"ticket_attachment_max_mb": current.TicketAttachmentMaxMB,
		"marketplace_enabled":      current.Marketplace.Enabled,
		"trade_hold_days":          current.Marketplace.TradeHoldDays,
		"contact":                  map[string]any{"intro": current.ContactIntro, "links": current.ContactLinks},
		"telegram_enabled":         current.Telegram.Ready(),
		"locale":                   map[string]any{"default_lang": current.Locale.DefaultLang, "usd_enabled": current.Locale.USDEnabled, "usd_rate": current.Locale.USDRate, "currencies": current.Locale.Currencies(), "default_currency": current.Locale.Currency()},
		"capabilities": []string{
			"accounts", "catalog", "billing", "provisioning", "clicd", "support", "audit", "notifications", "wallet", "marketplace",
		},
	}
}

// surfaceOf is the side of the site a request came through: nginx says so
// in a header; without nginx (development) the page tells.
func surfaceOf(r *http.Request) string {
	if surface := r.Header.Get("X-VPSBill-Surface"); surface != "" {
		return surface
	}
	switch surface := r.URL.Query().Get("surface"); surface {
	case "admin", "portal":
		return surface
	}
	return ""
}

// boot is everything a page needs before it can render: the site facts,
// whether installation is pending and who is signed in on this surface.
// nginx inlines it into index.html, so the first paint waits for no API
// round trip; the page fetches it itself when it is missing.
func (a *authenticator) boot(w http.ResponseWriter, r *http.Request) {
	surface := surfaceOf(r)
	data := map[string]any{
		"meta":             siteMeta(a.cfg, a.settings, r),
		"install_required": !a.settings.Current().Installed,
		"staff":            nil,
		"customer":         nil,
	}
	if a.settings.Current().Installed {
		if surface != "portal" {
			if cookie, err := r.Cookie(staffCookies.session); err == nil && cookie.Value != "" {
				if identity, err := a.store.SessionByToken(r.Context(), security.HashToken(cookie.Value)); err == nil {
					data["staff"] = identity.StaffIdentity
				}
			}
		}
		if surface != "admin" {
			if cookie, err := r.Cookie(customerCookies.session); err == nil && cookie.Value != "" {
				if identity, err := a.store.CustomerSessionByToken(r.Context(), security.HashToken(cookie.Value)); err == nil {
					data["customer"] = identity.CustomerIdentity
				}
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// siteLogo serves the uploaded logo. Its URL carries the version, so a
// matching request may be cached for good.
type siteLogo struct {
	settings *settings.Manager
	mu       sync.Mutex
	// cached holds the last image read for each variant.
	cached map[string]logoImage
}

type logoImage struct {
	version, kind string
	data          []byte
}

func (s *siteLogo) serve(w http.ResponseWriter, r *http.Request) {
	requested := r.URL.Query().Get("v")
	variant := r.URL.Query().Get("variant")
	if variant != "" && variant != "dark" && variant != "favicon" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	s.mu.Lock()
	if s.cached == nil {
		s.cached = map[string]logoImage{}
	}
	current, ok := s.cached[variant]
	if !ok || requested == "" || requested != current.version {
		kind, data, version, err := s.settings.LogoImage(r.Context(), variant)
		if err != nil {
			s.mu.Unlock()
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
			return
		}
		current = logoImage{version: version, kind: kind, data: data}
		s.cached[variant] = current
	}
	version, kind, data := current.version, current.kind, current.data
	s.mu.Unlock()
	if len(data) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	if requested != "" && requested == version {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	// An SVG opened on its own must not run scripts.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	w.Header().Set("Content-Type", kind)
	_, _ = w.Write(data)
}

func (a adminSettings) uploadLogo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, settings.LogoMaxBytes+64<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请选择不超过 512 KB 的图片"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, settings.LogoMaxBytes+1))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "无法读取上传的图片"})
		return
	}
	a.logoResult(w, a.settings.SetLogoImage(r.Context(), r.URL.Query().Get("variant"), data, principalFromContext(r.Context()).UserID))
}

func (a adminSettings) linkLogo(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	a.logoResult(w, a.settings.SetLogoURL(r.Context(), r.URL.Query().Get("variant"), input.URL, principalFromContext(r.Context()).UserID))
}

func (a adminSettings) logoMode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Mode string `json:"mode"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	a.logoResult(w, a.settings.SetLogoMode(r.Context(), input.Mode, principalFromContext(r.Context()).UserID))
}

func (a adminSettings) contact(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Intro string                 `json:"intro"`
		Links []settings.ContactLink `json:"links"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	err := a.settings.SetContact(r.Context(), input.Intro, input.Links, principalFromContext(r.Context()).UserID)
	switch {
	case errors.Is(err, settings.ErrInvalidSettings):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "settings_update_failed"})
	default:
		current := a.settings.Current()
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"intro": current.ContactIntro, "links": current.ContactLinks}})
	}
}

func (a adminSettings) logoResult(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrInvalidSettings):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "settings_update_failed"})
	default:
		current := a.settings.Current()
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{
			"logo_url": current.Logo(), "logo_external_url": current.LogoURL, "logo_mode": current.LogoMode,
			"logo_dark_url": current.LogoDark(), "logo_dark_external_url": current.LogoDarkURL,
			"favicon_url": current.Favicon(), "favicon_external_url": current.LogoFaviconURL,
		}})
	}
}
