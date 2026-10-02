package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/telegram"
)

// telegramAPI is the portal's Telegram link and the admin console's bot
// settings.
type telegramAPI struct {
	store    *postgres.TelegramStore
	settings *settings.Manager
	bot      *telegram.Bot
}

// telegramSettingsView is the bot settings without the token.
type telegramSettingsView struct {
	settings.TelegramSettings
	BotTokenConfigured bool                       `json:"bot_token_configured"`
	Status             telegram.Status            `json:"status"`
	Stats              postgres.TelegramSiteStats `json:"stats"`
}

func (a *telegramAPI) view(ctx context.Context) telegramSettingsView {
	runtime := a.settings.Current()
	// Staff read and write the amounts in the site's default currency.
	current := runtime.Telegram.In(runtime.Locale, runtime.Locale.Currency())
	view := telegramSettingsView{TelegramSettings: current, BotTokenConfigured: current.BotToken != "", Status: a.bot.Status()}
	view.BotToken = ""
	view.Stats, _ = a.store.SiteStats(ctx, time.Now())
	return view
}

func (a *telegramAPI) adminSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.view(r.Context())})
}

type telegramInput struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	// Chat is the group: a numeric id, an @username or a t.me link. Empty
	// keeps the current group.
	Chat              string `json:"chat"`
	ChatURL           string `json:"chat_url"`
	APIBase           string `json:"api_base"`
	BindRewardMinor   int64  `json:"bind_reward_minor"`
	CheckinMinMinor   int64  `json:"checkin_min_minor"`
	CheckinMaxMinor   int64  `json:"checkin_max_minor"`
	InviteRewardMinor int64  `json:"invite_reward_minor"`
	InviteHoldHours   int    `json:"invite_hold_hours"`
	InviteRequireLink bool   `json:"invite_require_link"`
	InviteDailyCap    int    `json:"invite_daily_cap"`
	DailyBudgetMinor  int64  `json:"daily_budget_minor"`
	ReplyTTLSeconds   int    `json:"reply_ttl_seconds"`
	Welcome           bool   `json:"welcome"`
	// RewardsFrom and RewardsUntil bound the period rewards are paid in;
	// null leaves that end open.
	RewardsFrom      *time.Time `json:"rewards_from"`
	RewardsUntil     *time.Time `json:"rewards_until"`
	RebatePercent    int        `json:"rebate_percent"`
	RebateMaxMinor   int64      `json:"rebate_max_minor"`
	RebateDelayHours int        `json:"rebate_delay_hours"`
	// AnnounceChat and AdminChat name a chat like Chat does; empty keeps
	// the current one and "0" turns it off.
	AnnounceChat     string `json:"announce_chat"`
	AnnounceNew      bool   `json:"announce_new"`
	AnnounceRestock  bool   `json:"announce_restock"`
	AnnounceHosted   bool   `json:"announce_hosted"`
	AnnounceDailyCap int    `json:"announce_daily_cap"`
	AdminChat        string `json:"admin_chat"`
}

// otherChat resolves the announcement or staff chat typed in the
// settings: empty keeps the current one, "0" clears it.
func (a *telegramAPI) otherChat(ctx context.Context, input telegramInput, typed string, id int64, title string) (int64, string, error) {
	typed = strings.TrimSpace(typed)
	switch typed {
	case "":
		return id, title, nil
	case "0":
		return 0, "", nil
	}
	token := strings.TrimSpace(input.BotToken)
	if token == "" {
		token = a.settings.Current().Telegram.BotToken
	}
	if token == "" {
		return 0, "", errors.New("请先填写 Bot Token")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	check, err := a.bot.Verify(ctx, input.APIBase, token, typed)
	if err != nil {
		return 0, "", err
	}
	if check.ChatID == 0 {
		return 0, "", errors.New("没有找到这个群或频道")
	}
	return check.ChatID, check.ChatTitle, nil
}

// check asks Telegram about the token and the group in the input, falling
// back to the stored ones.
func (a *telegramAPI) check(ctx context.Context, input telegramInput) (telegram.Check, string, error) {
	current := a.settings.Current().Telegram
	token := strings.TrimSpace(input.BotToken)
	if token == "" {
		token = current.BotToken
	}
	if token == "" {
		return telegram.Check{Warnings: []string{}}, "", nil
	}
	chat := strings.TrimSpace(input.Chat)
	if chat == "" && current.ChatID != 0 {
		chat = strconv.FormatInt(current.ChatID, 10)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	check, err := a.bot.Verify(ctx, input.APIBase, token, chat)
	return check, token, err
}

func (a *telegramAPI) adminCheck(w http.ResponseWriter, r *http.Request) {
	var input telegramInput
	if !decodeJSON(w, r, &input) {
		return
	}
	check, token, err := a.check(r.Context(), input)
	switch {
	case token == "":
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请先填写 Bot Token"})
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "telegram_failed", "message": "连接 Telegram 失败：" + err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": check})
	}
}

func (a *telegramAPI) adminUpdate(w http.ResponseWriter, r *http.Request) {
	var input telegramInput
	if !decodeJSON(w, r, &input) {
		return
	}
	current := a.settings.Current().Telegram
	next := settings.TelegramSettings{
		Enabled: input.Enabled, Currency: a.settings.Current().Locale.Currency(), BotToken: input.BotToken, APIBase: input.APIBase, ChatURL: strings.TrimSpace(input.ChatURL),
		BotUsername: current.BotUsername, ChatID: current.ChatID, ChatTitle: current.ChatTitle,
		BindRewardMinor: input.BindRewardMinor, CheckinMinMinor: input.CheckinMinMinor, CheckinMaxMinor: input.CheckinMaxMinor,
		InviteRewardMinor: input.InviteRewardMinor, InviteHoldHours: input.InviteHoldHours, InviteRequireLink: input.InviteRequireLink,
		InviteDailyCap: input.InviteDailyCap, DailyBudgetMinor: input.DailyBudgetMinor, ReplyTTLSeconds: input.ReplyTTLSeconds, Welcome: input.Welcome,
		RewardsFrom: input.RewardsFrom, RewardsUntil: input.RewardsUntil,
		RebatePercent: input.RebatePercent, RebateMaxMinor: input.RebateMaxMinor, RebateDelayHours: input.RebateDelayHours,
		AnnounceNew: input.AnnounceNew, AnnounceRestock: input.AnnounceRestock, AnnounceHosted: input.AnnounceHosted, AnnounceDailyCap: input.AnnounceDailyCap,
	}
	check, token, err := a.check(r.Context(), input)
	if err == nil {
		next.AnnounceChatID, next.AnnounceChatTitle, err = a.otherChat(r.Context(), input, input.AnnounceChat, current.AnnounceChatID, current.AnnounceChatTitle)
	}
	if err == nil {
		next.AdminChatID, next.AdminChatTitle, err = a.otherChat(r.Context(), input, input.AdminChat, current.AdminChatID, current.AdminChatTitle)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "telegram_failed", "message": "连接 Telegram 失败：" + err.Error()})
		return
	}
	if token != "" {
		next.BotUsername = check.BotUsername
		if check.ChatID != 0 {
			next.ChatID, next.ChatTitle = check.ChatID, check.ChatTitle
			if next.ChatURL == "" {
				next.ChatURL = check.ChatURL
			}
		}
	}
	err = a.settings.SetTelegram(r.Context(), next, principalFromContext(r.Context()).UserID)
	switch {
	case errors.Is(err, settings.ErrInvalidSettings):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "settings_update_failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"settings": a.view(r.Context()), "warnings": check.Warnings}})
	}
}

// customerTelegram is what the portal shows about Telegram: whether the
// customer linked an account, what the rewards are and what they earned.
func (a *telegramAPI) customerTelegram(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	cfg := a.settings.Current().TelegramLedger()
	data := map[string]any{
		"enabled":        cfg.Ready(),
		"email_verified": identity.EmailVerified,
		"linked":         nil,
	}
	if !cfg.Ready() {
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
		return
	}
	data["bot_username"], data["chat_url"], data["chat_title"] = cfg.BotUsername, cfg.ChatURL, cfg.ChatTitle
	data["rules"] = map[string]any{
		"bind_reward_minor": cfg.BindRewardMinor, "checkin_min_minor": cfg.CheckinMinMinor, "checkin_max_minor": cfg.CheckinMaxMinor,
		"invite_reward_minor": cfg.InviteRewardMinor, "invite_hold_hours": cfg.InviteHoldHours, "invite_require_link": cfg.InviteRequireLink,
		"invite_daily_cap": cfg.InviteDailyCap, "rebate_percent": cfg.RebatePercent, "rebate_max_minor": cfg.RebateMaxMinor,
	}
	// When rewards are paid: the portal says so wherever it offers them.
	data["rewards"] = map[string]any{"state": cfg.RewardsState(time.Now()), "from": cfg.RewardsFrom, "until": cfg.RewardsUntil}
	link, err := a.store.LinkByUser(r.Context(), identity.UserID)
	switch {
	case errors.Is(err, postgres.ErrTelegramNotLinked):
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	default:
		data["linked"] = link
		data["unlink_after"] = link.LinkedAt.Add(postgres.TelegramUnlinkCooldown)
		data["invite_link"], _ = a.store.InviteLink(r.Context(), identity.AccountID, cfg.ChatID)
	}
	stats, err := a.store.AccountStats(r.Context(), identity.AccountID, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	data["stats"] = stats
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// customerNotifications says where the customer's notices go and which
// channels can be chosen.
func (a *telegramAPI) customerNotifications(w http.ResponseWriter, r *http.Request) {
	a.writeNotifications(w, r)
}

func (a *telegramAPI) writeNotifications(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	channels, err := a.store.NotifyChannels(r.Context(), identity.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_, linkErr := a.store.LinkByUser(r.Context(), identity.UserID)
	current := a.settings.Current()
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"email": channels.Email, "telegram": channels.Telegram,
		"telegram_linked": linkErr == nil, "telegram_enabled": current.Telegram.Ready(), "mail_enabled": current.SMTP.Configured(),
	}})
}

// customerSetNotifications stores the customer's channels: mail, Telegram
// or both, never neither.
func (a *telegramAPI) customerSetNotifications(w http.ResponseWriter, r *http.Request) {
	var input postgres.NotifyChannels
	if !decodeJSON(w, r, &input) {
		return
	}
	err := a.store.SetNotifyChannels(r.Context(), customerPrincipalFromContext(r.Context()).UserID, input)
	switch {
	case errors.Is(err, postgres.ErrNotifyChannels):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "邮件和 Telegram 至少要选一种通知方式"})
	case errors.Is(err, postgres.ErrTelegramNotLinked):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "not_linked", "message": "请先绑定 Telegram，再选择用 Telegram 接收通知"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		a.writeNotifications(w, r)
	}
}

// customerBind makes a link that opens the bot and ties the Telegram
// account that follows it to this customer.
func (a *telegramAPI) customerBind(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	cfg := a.settings.Current().Telegram
	if !cfg.Ready() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "telegram_disabled", "message": "Telegram 绑定暂未开启"})
		return
	}
	// Telegram start parameters take 64 characters of [A-Za-z0-9_-].
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	code := hex.EncodeToString(raw)
	expires := time.Now().Add(telegram.BindCodeTTL)
	err := a.store.CreateBindCode(r.Context(), identity.UserID, security.HashToken(code), expires)
	switch {
	case errors.Is(err, postgres.ErrTelegramAlreadyLinked):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already_linked", "message": "你的账号已经绑定了 Telegram"})
	case errors.Is(err, postgres.ErrEmailUnverified):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "email_unverified", "message": "请先完成邮箱验证，再绑定 Telegram"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"url":        "https://t.me/" + cfg.BotUsername + "?start=bind_" + code,
			"expires_at": expires,
		}})
	}
}

func (a *telegramAPI) customerUnlink(w http.ResponseWriter, r *http.Request) {
	err := a.store.Unlink(r.Context(), customerPrincipalFromContext(r.Context()).UserID, time.Now())
	switch {
	case errors.Is(err, postgres.ErrTelegramNotLinked):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_linked", "message": "你的账号没有绑定 Telegram"})
	case errors.Is(err, postgres.ErrTelegramCooldown):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "unlink_cooldown", "message": "绑定满 7 天后才能解绑"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// customerInviteLink makes the customer's own invite link to the group.
func (a *telegramAPI) customerInviteLink(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	if _, err := a.store.LinkByUser(r.Context(), identity.UserID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "not_linked", "message": "请先绑定 Telegram"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	link, err := a.bot.InviteLink(ctx, identity.AccountID)
	switch {
	case errors.Is(err, telegram.ErrNotReady):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "invite_disabled", "message": "邀请奖励暂未开启"})
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "telegram_failed", "message": "暂时无法生成邀请链接，请稍后再试或联系客服"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"invite_link": link}})
	}
}
