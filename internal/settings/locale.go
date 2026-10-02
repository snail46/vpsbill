package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// LocaleSettings is the site's display language and currency. The ledger
// is kept in CNY; USD is a display currency converted at USDRate, and
// every payment is charged in CNY.
type LocaleSettings struct {
	// DefaultLang is the language a visitor sees before choosing one, when
	// the browser's language is neither Chinese nor English.
	DefaultLang string `json:"default_lang"`
	// USDEnabled offers the USD display currency in the portal.
	USDEnabled bool `json:"usd_enabled"`
	// USDRate is how many CNY one USD is worth.
	USDRate float64 `json:"usd_rate"`
	// USDRateAuto follows the market rate (see internal/fx).
	USDRateAuto      bool       `json:"usd_rate_auto"`
	USDRateUpdatedAt *time.Time `json:"usd_rate_updated_at,omitempty"`
}

const (
	minUSDRate = 1
	maxUSDRate = 100
)

func DefaultLocaleSettings() LocaleSettings {
	return LocaleSettings{DefaultLang: "zh", USDEnabled: true, USDRate: 7.2, USDRateAuto: true}
}

func decodeLocaleSettings(raw []byte) LocaleSettings {
	value := DefaultLocaleSettings()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	if value.DefaultLang != "en" {
		value.DefaultLang = "zh"
	}
	if value.USDRate < minUSDRate || value.USDRate > maxUSDRate {
		value.USDRate = DefaultLocaleSettings().USDRate
	}
	return value
}

// Lang is "zh" or "en": the given language when it is one of them, the
// site's default otherwise.
func (r Runtime) Lang(preferred string) string {
	switch preferred = strings.ToLower(strings.TrimSpace(preferred)); {
	case strings.HasPrefix(preferred, "zh"):
		return "zh"
	case strings.HasPrefix(preferred, "en"):
		return "en"
	}
	if r.Locale.DefaultLang == "en" {
		return "en"
	}
	return "zh"
}

// SetLocale stores the language and currency settings. A rate typed by
// staff is stamped now; with USDRateAuto the next update replaces it.
func (m *Manager) SetLocale(ctx context.Context, in LocaleSettings, actorID string) error {
	if in.DefaultLang != "zh" && in.DefaultLang != "en" {
		return fmt.Errorf("%w: 默认语言只能是中文或英文", ErrInvalidSettings)
	}
	if math.IsNaN(in.USDRate) || in.USDRate < minUSDRate || in.USDRate > maxUSDRate {
		return fmt.Errorf("%w: 美元汇率必须在 %d–%d 之间", ErrInvalidSettings, minUSDRate, maxUSDRate)
	}
	in.USDRate = roundRate(in.USDRate)
	current := m.Current().Locale
	in.USDRateUpdatedAt = current.USDRateUpdatedAt
	if in.USDRate != current.USDRate || in.USDRateUpdatedAt == nil {
		now := time.Now().UTC()
		in.USDRateUpdatedAt = &now
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return m.updateLogo(ctx, `locale_settings=$1`, "locale_settings.updated", actorID, body)
}

// RecordUSDRate stores a rate read from the market; it is ignored when
// staff turned automatic updates off meanwhile.
func (m *Manager) RecordUSDRate(ctx context.Context, rate float64) error {
	if math.IsNaN(rate) || rate < minUSDRate || rate > maxUSDRate {
		return fmt.Errorf("usd rate %v is out of range", rate)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.Current().Locale
	if !current.USDRateAuto {
		return nil
	}
	now := time.Now().UTC()
	current.USDRate, current.USDRateUpdatedAt = roundRate(rate), &now
	body, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if _, err := m.db.Exec(ctx, `UPDATE system_settings SET locale_settings=$1,updated_at=now() WHERE singleton=true`, body); err != nil {
		return err
	}
	return m.reload(ctx)
}

func roundRate(rate float64) float64 { return math.Round(rate*10000) / 10000 }
