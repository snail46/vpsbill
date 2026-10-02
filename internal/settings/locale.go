package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"vpsbill/internal/store/postgres"
)

// LocaleSettings is the site's display language and currencies. The ledger
// is kept in LedgerCurrency; amounts shown in the other currency are
// converted at USDRate.
type LocaleSettings struct {
	// LedgerCurrency is the currency of every balance, price and invoice:
	// "CNY" or "USD". Only SwitchLedger changes it.
	LedgerCurrency string `json:"ledger_currency"`
	// DefaultLang is the language a visitor sees before choosing one, when
	// the browser's language is neither Chinese nor English.
	DefaultLang string `json:"default_lang"`
	// CNYHidden and USDEnabled say which currencies the portal offers; at
	// least one is shown.
	CNYHidden  bool `json:"cny_hidden"`
	USDEnabled bool `json:"usd_enabled"`
	// DefaultCurrency is the currency visitors see before choosing one,
	// and the one top-ups and Telegram rewards are quoted in.
	DefaultCurrency string `json:"default_currency"`
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
	return LocaleSettings{LedgerCurrency: "CNY", DefaultLang: "zh", USDEnabled: true, DefaultCurrency: "CNY", USDRate: 7.2, USDRateAuto: true}
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
	if value.CNYHidden && !value.USDEnabled {
		value.CNYHidden = false
	}
	value.DefaultCurrency = value.Currency()
	value.LedgerCurrency = value.Ledger()
	return value
}

// Ledger is the currency the books are kept in.
func (l LocaleSettings) Ledger() string {
	if l.LedgerCurrency == "USD" {
		return "USD"
	}
	return "CNY"
}

// Currency is the site's default currency, always one that is shown.
func (l LocaleSettings) Currency() string {
	switch {
	case l.CNYHidden:
		return "USD"
	case l.DefaultCurrency == "USD" && l.USDEnabled:
		return "USD"
	}
	return "CNY"
}

// Currencies lists the currencies the portal offers.
func (l LocaleSettings) Currencies() []string {
	shown := make([]string, 0, 2)
	if !l.CNYHidden {
		shown = append(shown, "CNY")
	}
	if l.USDEnabled {
		shown = append(shown, "USD")
	}
	return shown
}

// ToLedger turns an amount in a currency into ledger minor units.
func (l LocaleSettings) ToLedger(minor int64, currency string) int64 {
	return l.Convert(minor, currency, l.Ledger())
}

// FromLedger turns ledger minor units into an amount in a currency.
func (l LocaleSettings) FromLedger(minor int64, currency string) int64 {
	return l.Convert(minor, l.Ledger(), currency)
}

// Convert moves an amount between CNY and USD at the site's rate ("" is
// CNY).
func (l LocaleSettings) Convert(minor int64, from, to string) int64 {
	fromUSD, toUSD := from == "USD", to == "USD"
	switch {
	case fromUSD == toUSD || l.USDRate <= 0:
		return minor
	case fromUSD:
		return int64(math.Round(float64(minor) * l.USDRate))
	}
	return int64(math.Round(float64(minor) / l.USDRate))
}

// Money prints a ledger amount in the site's default currency.
func (l LocaleSettings) Money(ledgerMinor int64) string {
	currency := l.Currency()
	return FormatMoney(l.FromLedger(ledgerMinor, currency), currency)
}

// FormatMoney prints minor units with the currency's sign.
func FormatMoney(minor int64, currency string) string {
	sign, symbol := "", "¥"
	if minor < 0 {
		sign, minor = "-", -minor
	}
	if currency == "USD" {
		symbol = "$"
	}
	return fmt.Sprintf("%s%s%d.%02d", sign, symbol, minor/100, minor%100)
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
	switch {
	case in.CNYHidden && !in.USDEnabled:
		return fmt.Errorf("%w: 至少要显示一种币种", ErrInvalidSettings)
	case in.DefaultCurrency != "CNY" && in.DefaultCurrency != "USD":
		return fmt.Errorf("%w: 默认币种只能是 CNY 或 USD", ErrInvalidSettings)
	case in.DefaultCurrency == "CNY" && in.CNYHidden, in.DefaultCurrency == "USD" && !in.USDEnabled:
		return fmt.Errorf("%w: 默认币种必须是正在显示的币种", ErrInvalidSettings)
	}
	in.USDRate = roundRate(in.USDRate)
	current := m.Current().Locale
	in.LedgerCurrency = current.Ledger()
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

// SwitchLedger changes the currency the books are kept in. Every stored
// amount is converted at the current rate in one transaction (see
// postgres.ConvertLedger); the new currency is shown in the portal and
// becomes the default. It fails with postgres.ErrLedgerBusy while online
// payments are under way.
func (m *Manager) SwitchLedger(ctx context.Context, to, actorID string) (postgres.LedgerConversion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var counts postgres.LedgerConversion
	if to != "CNY" && to != "USD" {
		return counts, fmt.Errorf("%w: 记账币种只能是 CNY 或 USD", ErrInvalidSettings)
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback(ctx)
	// The stored settings, not this instance's copy, say what the ledger
	// is in; the row lock keeps two switches apart.
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT locale_settings FROM system_settings WHERE singleton=true FOR UPDATE`).Scan(&raw); err != nil {
		return counts, err
	}
	locale := decodeLocaleSettings(raw)
	from := locale.Ledger()
	if from == to {
		return counts, fmt.Errorf("%w: 账本已经是 %s", ErrInvalidSettings, to)
	}
	if counts, err = postgres.ConvertLedger(ctx, tx, from, to, locale.USDRate); err != nil {
		return counts, err
	}
	locale.LedgerCurrency, locale.DefaultCurrency = to, to
	if to == "USD" {
		locale.USDEnabled = true
	} else {
		locale.CNYHidden = false
	}
	body, err := json.Marshal(locale)
	if err != nil {
		return counts, err
	}
	if _, err = tx.Exec(ctx, `UPDATE system_settings SET locale_settings=$1,updated_at=now() WHERE singleton=true`, body); err != nil {
		return counts, err
	}
	summary, err := json.Marshal(counts)
	if err != nil {
		return counts, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ledger_switches(from_currency,to_currency,usd_rate,actor_user_id,counts) VALUES($1,$2,$3,nullif($4,'')::uuid,$5)`, from, to, locale.USDRate, actorID, summary); err != nil {
		return counts, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',nullif($1,'')::uuid,'ledger.currency_switched','system_settings','singleton',jsonb_build_object('from',$2::text,'to',$3::text,'usd_rate',$4::float8))`, actorID, from, to, locale.USDRate); err != nil {
		return counts, err
	}
	if err = tx.Commit(ctx); err != nil {
		return counts, err
	}
	return counts, m.reload(ctx)
}

// LedgerState is what a switch of the ledger currency would touch.
func (m *Manager) LedgerState(ctx context.Context) (postgres.LedgerState, error) {
	return postgres.ReadLedgerState(ctx, m.db)
}
