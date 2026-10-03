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
	// RewardsFrom and RewardsUntil bound when rewards are paid (nil = no
	// bound). Outside the period accounts can still be linked, for
	// notices, but nothing is paid.
	RewardsFrom  *time.Time `json:"rewards_from"`
	RewardsUntil *time.Time `json:"rewards_until"`
	// RebatePercent of an invited member's first real payment goes to the
	// inviter (0 = off), at most RebateMaxMinor (0 = no cap), once the
	// payment is RebateDelayHours old.
	RebatePercent    int   `json:"rebate_percent"`
	RebateMaxMinor   int64 `json:"rebate_max_minor"`
	RebateDelayHours int   `json:"rebate_delay_hours"`
	// AnnounceChatID is the channel or group that hears about new plans
	// and restocks (0 = nowhere).
	AnnounceChatID    int64  `json:"announce_chat_id"`
	AnnounceChatTitle string `json:"announce_chat_title"`
	AnnounceNew       bool   `json:"announce_new"`
	AnnounceRestock   bool   `json:"announce_restock"`
	// AnnounceHosted includes plans sold by hosts.
	AnnounceHosted bool `json:"announce_hosted"`
	// AnnounceDailyCap is how many announcements go out per day (0 = no cap).
	AnnounceDailyCap int `json:"announce_daily_cap"`
	// AdminChatID is the staff group that gets what merchant mail says
	// (0 = none).
	AdminChatID    int64  `json:"admin_chat_id"`
	AdminChatTitle string `json:"admin_chat_title"`

	// Tickets lets customers open and answer tickets in a private chat
	// with the bot, and staff answer them in the staff group, by replying
	// to the bot's notice.
	Tickets bool `json:"tickets"`
	// StreakBonuses pay extra when a check-in makes a streak of so many
	// days (and each multiple of it).
	StreakBonuses []StreakBonus `json:"streak_bonuses"`
	// Leaderboards posts last week's check-in and invite boards in the
	// group on Monday at 10:00 (UTC+8), paying LeaderboardPrizes by rank.
	Leaderboards      bool    `json:"leaderboards"`
	LeaderboardPrizes []int64 `json:"leaderboard_prizes"`
	// Red packets sent from the group by staff take these conditions:
	// only accounts that paid for something, linked at least so many days.
	RedPacketRequireSpent  bool `json:"red_packet_require_spent"`
	RedPacketMinLinkedDays int  `json:"red_packet_min_linked_days"`
	// Verify holds new members silent until they press a button
	// ("button") or link a site account ("link"); "" lets everyone in.
	// Who does not pass within VerifyMinutes is removed.
	Verify        string `json:"verify"`
	VerifyMinutes int    `json:"verify_minutes"`
	// FilterLinks deletes links posted by members without a linked site
	// account; BlockedWords deletes messages with any of the words;
	// UnlinkedPerMinute caps the messages of members without a linked
	// account (0 = no cap). Group administrators and linked staff are
	// exempt.
	FilterLinks       bool     `json:"filter_links"`
	BlockedWords      []string `json:"blocked_words"`
	UnlinkedPerMinute int      `json:"unlinked_per_minute"`
}

// StreakBonus is paid for checking in Days days in a row.
type StreakBonus struct {
	Days        int   `json:"days"`
	AmountMinor int64 `json:"amount_minor"`
}

// Verification modes for new members.
const (
	VerifyButton = "button"
	VerifyLink   = "link"
)

// Rewards periods, as RewardsState reports them.
const (
	RewardsOpen     = "open"
	RewardsUpcoming = "upcoming"
	RewardsEnded    = "ended"
)

// RewardsState says whether rewards are paid at a moment.
func (t TelegramSettings) RewardsState(now time.Time) string {
	switch {
	case t.RewardsFrom != nil && now.Before(*t.RewardsFrom):
		return RewardsUpcoming
	case t.RewardsUntil != nil && !now.Before(*t.RewardsUntil):
		return RewardsEnded
	}
	return RewardsOpen
}

// CanSend reports whether the bot can send messages at all, whether or
// not rewards are on.
func (t TelegramSettings) CanSend() bool { return t.BotToken != "" }

func DefaultTelegramSettings() TelegramSettings {
	return TelegramSettings{
		BindRewardMinor: 100, CheckinMinMinor: 10, CheckinMaxMinor: 50,
		InviteRewardMinor: 100, InviteHoldHours: 24, InviteRequireLink: true, InviteDailyCap: 10,
		DailyBudgetMinor: 10000, ReplyTTLSeconds: 60, Welcome: true,
		RebateMaxMinor: 2000, RebateDelayHours: 72, AnnounceNew: true, AnnounceRestock: true, AnnounceDailyCap: 10,
		Tickets: true, StreakBonuses: []StreakBonus{{Days: 7, AmountMinor: 100}, {Days: 30, AmountMinor: 500}},
		LeaderboardPrizes: []int64{500, 300, 100}, VerifyMinutes: 10,
	}
}

// In returns the settings with every amount converted to a currency.
func (t TelegramSettings) In(locale LocaleSettings, currency string) TelegramSettings {
	from := t.Currency
	if from == "" {
		from = "CNY"
	}
	for _, amount := range []*int64{&t.BindRewardMinor, &t.CheckinMinMinor, &t.CheckinMaxMinor, &t.InviteRewardMinor, &t.DailyBudgetMinor, &t.RebateMaxMinor} {
		*amount = locale.Convert(*amount, from, currency)
	}
	// The lists are copied: the stored settings must not change.
	bonuses := make([]StreakBonus, len(t.StreakBonuses))
	for i, bonus := range t.StreakBonuses {
		bonuses[i] = StreakBonus{Days: bonus.Days, AmountMinor: locale.Convert(bonus.AmountMinor, from, currency)}
	}
	t.StreakBonuses = bonuses
	prizes := make([]int64, len(t.LeaderboardPrizes))
	for i, prize := range t.LeaderboardPrizes {
		prizes[i] = locale.Convert(prize, from, currency)
	}
	t.LeaderboardPrizes = prizes
	t.Currency = currency
	return t
}

// TelegramLedger is the Telegram settings with every amount in ledger
// minor units, which is what rewards are paid in.
func (r Runtime) TelegramLedger() TelegramSettings { return r.Telegram.In(r.Locale, r.Locale.Ledger()) }

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
	case t.RewardsFrom != nil && t.RewardsUntil != nil && !t.RewardsUntil.After(*t.RewardsFrom):
		return fmt.Errorf("活动结束时间必须晚于开始时间")
	case t.RebatePercent < 0 || t.RebatePercent > 50:
		return fmt.Errorf("首单返利比例必须在 0–50 之间")
	case t.RebateMaxMinor < 0 || t.RebateMaxMinor > 10*maxTelegramReward:
		return fmt.Errorf("首单返利上限超出范围")
	case t.RebateDelayHours < 0 || t.RebateDelayHours > 720:
		return fmt.Errorf("首单返利的等待时间必须在 0–720 小时之间")
	case t.AnnounceDailyCap < 0 || t.AnnounceDailyCap > 200:
		return fmt.Errorf("每日推送条数上限必须在 0–200 之间")
	case t.ReplyTTLSeconds < 0 || t.ReplyTTLSeconds > 3600:
		return fmt.Errorf("群内回复的保留时间必须在 0–3600 秒之间")
	case t.APIBase != "" && absoluteURL(t.APIBase, true) != nil:
		return fmt.Errorf("Bot API 地址必须是完整的 http(s):// 地址，或者留空")
	case t.ChatURL != "" && absoluteURL(t.ChatURL, true) != nil:
		return fmt.Errorf("群链接必须是完整的 https://t.me/… 地址，或者留空")
	case len(t.StreakBonuses) > 5:
		return fmt.Errorf("连续签到奖励最多设置 5 档")
	case len(t.LeaderboardPrizes) > 10:
		return fmt.Errorf("排行榜奖金最多设置前 10 名")
	case t.RedPacketMinLinkedDays < 0 || t.RedPacketMinLinkedDays > 365:
		return fmt.Errorf("红包要求的绑定天数必须在 0–365 之间")
	case t.Verify != "" && t.Verify != VerifyButton && t.Verify != VerifyLink:
		return fmt.Errorf("入群验证方式无效")
	case t.VerifyMinutes < 1 || t.VerifyMinutes > 1440:
		return fmt.Errorf("入群验证时限必须在 1–1440 分钟之间")
	case len(t.BlockedWords) > 200:
		return fmt.Errorf("屏蔽词最多 200 个")
	case t.UnlinkedPerMinute < 0 || t.UnlinkedPerMinute > 60:
		return fmt.Errorf("未绑定成员每分钟发言上限必须在 0–60 之间")
	case t.Enabled && t.BotToken == "":
		return fmt.Errorf("启用前需要填写 Bot Token")
	case t.Enabled && t.ChatID == 0:
		return fmt.Errorf("启用前需要设置群")
	}
	return nil
}

// validateLists checks the streak bonuses and prizes.
func (t TelegramSettings) validateLists() error {
	seen := map[int]bool{}
	for _, bonus := range t.StreakBonuses {
		switch {
		case bonus.Days < 2 || bonus.Days > 365:
			return fmt.Errorf("连续签到天数必须在 2–365 之间")
		case seen[bonus.Days]:
			return fmt.Errorf("连续签到天数不能重复")
		case bonus.AmountMinor < 0 || bonus.AmountMinor > maxTelegramReward:
			return fmt.Errorf("连续签到奖励必须在 0–%d 之间", maxTelegramReward/100)
		}
		seen[bonus.Days] = true
	}
	for _, prize := range t.LeaderboardPrizes {
		if prize < 0 || prize > maxTelegramReward {
			return fmt.Errorf("排行榜奖金必须在 0–%d 之间", maxTelegramReward/100)
		}
	}
	for _, word := range t.BlockedWords {
		if len([]rune(word)) > 40 {
			return fmt.Errorf("屏蔽词「%s」太长（最多 40 个字）", word)
		}
	}
	return nil
}

// cleanWords trims the blocked words and drops empty and repeated ones.
func cleanWords(words []string) []string {
	result := make([]string, 0, len(words))
	seen := map[string]bool{}
	for _, word := range words {
		word = strings.TrimSpace(word)
		key := strings.ToLower(word)
		if word == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, word)
	}
	return result
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
	in.BlockedWords = cleanWords(in.BlockedWords)
	if in.VerifyMinutes == 0 {
		in.VerifyMinutes = 10
	}
	if err := in.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	if err := in.validateLists(); err != nil {
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
