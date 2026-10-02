package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TelegramSettings configures the Telegram bot and what it pays. Amounts
// are minor units of Currency, the site's default currency when they were
// saved; rewards are converted to the ledger when they are paid.
type TelegramSettings struct {
	Enabled bool `json:"enabled"`
	// Currency is "CNY" or "USD" ("" = CNY).
	Currency string `json:"currency"`
	BotToken string `json:"bot_token,omitempty"`
	// BotUsername and ChatTitle are read from Telegram when the settings
	// are saved.
	BotUsername string `json:"bot_username"`
	// ChatID is the group rewards are earned in.
	ChatID    int64  `json:"chat_id"`
	ChatTitle string `json:"chat_title"`
	// ChatURL is the group's public join link shown in the portal.
	ChatURL string `json:"chat_url"`
	// APIBase replaces https://api.telegram.org, for servers that reach
	// Telegram through a reverse proxy.
	APIBase string `json:"api_base"`
	// BindRewardMinor is paid once for linking an account.
	BindRewardMinor int64 `json:"bind_reward_minor"`
	// A check-in pays a random amount between the two.
	CheckinMinMinor int64 `json:"checkin_min_minor"`
	CheckinMaxMinor int64 `json:"checkin_max_minor"`
	// InviteRewardMinor is paid for a member who joined through the
	// account's invite link, stayed InviteHoldHours and, with
	// InviteRequireLink, linked a verified site account.
	InviteRewardMinor int64 `json:"invite_reward_minor"`
	InviteHoldHours   int   `json:"invite_hold_hours"`
	InviteRequireLink bool  `json:"invite_require_link"`
	// InviteDailyCap is how many invitations one account is paid for per
	// day (0 = no cap).
	InviteDailyCap int `json:"invite_daily_cap"`
	// DailyBudgetMinor caps all rewards paid in one day (0 = no cap).
	DailyBudgetMinor int64 `json:"daily_budget_minor"`
	// ReplyTTLSeconds deletes the bot's replies in the group after this
	// long (0 = keep them).
	ReplyTTLSeconds int `json:"reply_ttl_seconds"`
	// Welcome greets new members with how to earn balance.
	Welcome bool `json:"welcome"`
}

func DefaultTelegramSettings() TelegramSettings {
	return TelegramSettings{
		BindRewardMinor: 100, CheckinMinMinor: 10, CheckinMaxMinor: 50,
		InviteRewardMinor: 100, InviteHoldHours: 24, InviteRequireLink: true, InviteDailyCap: 10,
		DailyBudgetMinor: 10000, ReplyTTLSeconds: 60, Welcome: true,
	}
}

// In returns the settings with every amount converted to a currency.
func (t TelegramSettings) In(locale LocaleSettings, currency string) TelegramSettings {
	from := t.Currency
	if from == "" {
		from = "CNY"
	}
	for _, amount := range []*int64{&t.BindRewardMinor, &t.CheckinMinMinor, &t.CheckinMaxMinor, &t.InviteRewardMinor, &t.DailyBudgetMinor} {
		*amount = locale.Convert(*amount, from, currency)
	}
	t.Currency = currency
	return t
}

// TelegramLedger is the Telegram settings with every amount in ledger
// (CNY) minor units, which is what rewards are paid in.
func (r Runtime) TelegramLedger() TelegramSettings { return r.Telegram.In(r.Locale, "CNY") }

// Ready reports whether the bot can run.
func (t TelegramSettings) Ready() bool {
	return t.Enabled && t.BotToken != "" && t.BotUsername != "" && t.ChatID != 0
}

// InviteHold is how long an invited member must stay.
func (t TelegramSettings) InviteHold() time.Duration {
	return time.Duration(t.InviteHoldHours) * time.Hour
}

// maxTelegramReward keeps a typo from paying out a fortune: 1000 of the
// currency.
const maxTelegramReward = 100_000

func (t TelegramSettings) validate() error {
	amounts := []int64{t.BindRewardMinor, t.CheckinMinMinor, t.CheckinMaxMinor, t.InviteRewardMinor}
	for _, amount := range amounts {
		if amount < 0 || amount > maxTelegramReward {
			return fmt.Errorf("奖励金额必须在 0–%d 之间", maxTelegramReward/100)
		}
	}
	switch {
	case t.CheckinMinMinor > t.CheckinMaxMinor:
		return fmt.Errorf("签到奖励的最小值不能大于最大值")
	case t.InviteHoldHours < 0 || t.InviteHoldHours > 720:
		return fmt.Errorf("邀请奖励的留群时间必须在 0–720 小时之间")
	case t.InviteDailyCap < 0 || t.InviteDailyCap > 1000:
		return fmt.Errorf("每人每日邀请奖励上限必须在 0–1000 之间")
	case t.DailyBudgetMinor < 0 || t.DailyBudgetMinor > 100_000_000:
		return fmt.Errorf("每日奖励总预算超出范围")
	case t.ReplyTTLSeconds < 0 || t.ReplyTTLSeconds > 3600:
		return fmt.Errorf("群内回复的保留时间必须在 0–3600 秒之间")
	case t.APIBase != "" && absoluteURL(t.APIBase, true) != nil:
		return fmt.Errorf("Bot API 地址必须是完整的 http(s):// 地址，或者留空")
	case t.ChatURL != "" && absoluteURL(t.ChatURL, true) != nil:
		return fmt.Errorf("群链接必须是完整的 https://t.me/… 地址，或者留空")
	case t.Enabled && t.BotToken == "":
		return fmt.Errorf("启用前需要填写 Bot Token")
	case t.Enabled && t.ChatID == 0:
		return fmt.Errorf("启用前需要设置群")
	}
	return nil
}

func (m *Manager) decodeTelegram(sealed []byte) (TelegramSettings, error) {
	value := DefaultTelegramSettings()
	if len(sealed) == 0 {
		return value, nil
	}
	plain, err := m.box.Open(sealed)
	if err != nil {
		return value, fmt.Errorf("decrypt telegram settings: %w", err)
	}
	if err := json.Unmarshal([]byte(plain), &value); err != nil {
		return value, fmt.Errorf("decode telegram settings: %w", err)
	}
	return value, nil
}

// SetTelegram stores the bot settings, sealed because they hold the bot's
// token. An empty token keeps the stored one.
func (m *Manager) SetTelegram(ctx context.Context, in TelegramSettings, actorID string) error {
	in.BotToken = strings.TrimSpace(in.BotToken)
	in.APIBase = strings.TrimRight(strings.TrimSpace(in.APIBase), "/")
	in.ChatURL = strings.TrimSpace(in.ChatURL)
	if in.BotToken == "" {
		in.BotToken = m.Current().Telegram.BotToken
	}
	if in.Currency != "USD" {
		in.Currency = "CNY"
	}
	if err := in.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	plain, err := json.Marshal(in)
	if err != nil {
		return err
	}
	sealed, err := m.box.Seal(string(plain))
	if err != nil {
		return err
	}
	// Another bot numbers its updates from scratch.
	newBot := in.BotToken != m.Current().Telegram.BotToken
	return m.updateLogo(ctx, `telegram_settings_encrypted=$1,telegram_update_offset=CASE WHEN $2::boolean THEN 0 ELSE telegram_update_offset END`, "telegram_settings.updated", actorID, sealed, newBot)
}
