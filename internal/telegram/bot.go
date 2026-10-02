package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// ErrNotReady is returned while the bot is switched off or not set up.
var ErrNotReady = errors.New("telegram bot is not ready")

const (
	idleWait       = 10 * time.Second
	errorWait      = 5 * time.Second
	settleInterval = time.Minute
	// BindCodeTTL is how long a bind link works.
	BindCodeTTL = 10 * time.Minute
)

// API is the part of the Bot API the bot uses.
type API interface {
	GetMe(ctx context.Context) (User, error)
	GetUpdates(ctx context.Context, offset int64) ([]Update, error)
	SendMessage(ctx context.Context, chatID int64, html string, replyTo int64) (Message, error)
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
	GetChat(ctx context.Context, chat any) (Chat, error)
	GetChatMember(ctx context.Context, chatID, userID int64) (ChatMember, error)
	CreateInviteLink(ctx context.Context, chatID int64, name string) (string, error)
}

// SeenChat is a group the bot was added to, offered in the settings.
type SeenChat struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

// Status is how the bot is doing, for the admin console.
type Status struct {
	Running    bool       `json:"running"`
	LastError  string     `json:"last_error"`
	LastPollAt *time.Time `json:"last_poll_at,omitempty"`
	Chats      []SeenChat `json:"chats"`
}

type Bot struct {
	store    *postgres.TelegramStore
	settings *settings.Manager
	logger   *slog.Logger
	connect  func(apiBase, token string) API
	now      func() time.Time
	// between picks a check-in reward in [min, max].
	between func(min, max int64) int64

	mu      sync.Mutex
	status  Status
	chats   map[int64]SeenChat
	members map[int64]struct{}
	// inviteMu makes each account's invite link once.
	inviteMu sync.Mutex
}

func New(store *postgres.TelegramStore, runtime *settings.Manager, logger *slog.Logger) *Bot {
	return &Bot{
		store: store, settings: runtime, logger: logger, now: time.Now,
		connect: func(apiBase, token string) API { return NewClient(apiBase, token) },
		between: func(min, max int64) int64 {
			if max <= min {
				return min
			}
			return min + rand.Int64N(max-min+1)
		},
		chats: map[int64]SeenChat{}, members: map[int64]struct{}{},
	}
}

func (b *Bot) client(cfg settings.TelegramSettings) API { return b.connect(cfg.APIBase, cfg.BotToken) }

// Status reports whether the bot is polling and the groups it saw.
func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := b.status
	status.Chats = make([]SeenChat, 0, len(b.chats))
	for _, chat := range b.chats {
		status.Chats = append(status.Chats, chat)
	}
	sort.Slice(status.Chats, func(i, j int) bool { return status.Chats[i].Title < status.Chats[j].Title })
	return status
}

func (b *Bot) setStatus(running bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.Running = running
	if err != nil {
		b.status.LastError = err.Error()
	} else if running {
		b.status.LastError = ""
		now := b.now()
		b.status.LastPollAt = &now
	}
}

func sleep(ctx context.Context, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// Run polls Telegram while a bot token is set, until ctx ends. It runs
// without the feature being enabled too, so the settings page can list
// the groups the bot was added to.
func (b *Bot) Run(ctx context.Context) {
	for ctx.Err() == nil {
		current := b.settings.Current()
		if !current.Installed || current.Telegram.BotToken == "" {
			b.setStatus(false, nil)
			sleep(ctx, idleWait)
			continue
		}
		// Telegram gives a bot's updates to one reader only.
		release, locked, err := b.store.PollLock(ctx)
		if err != nil || !locked {
			sleep(ctx, 3*idleWait)
			continue
		}
		b.poll(ctx, current.TelegramLedger())
		release()
		b.setStatus(false, nil)
	}
}

// poll reads updates until the bot's token or API address changes.
func (b *Bot) poll(ctx context.Context, cfg settings.TelegramSettings) {
	client := b.client(cfg)
	offset, err := b.store.UpdateOffset(ctx)
	if err != nil {
		b.logger.Error("read telegram update offset", "error", err)
	}
	var settled time.Time
	for ctx.Err() == nil {
		current := b.settings.Current().TelegramLedger()
		if current.BotToken != cfg.BotToken || current.APIBase != cfg.APIBase {
			return
		}
		cfg = current
		updates, err := client.GetUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.setStatus(true, err)
			b.logger.Warn("telegram getUpdates", "error", err)
			sleep(ctx, errorWait)
			continue
		}
		b.setStatus(true, nil)
		for _, update := range updates {
			b.handle(ctx, client, cfg, update)
			offset = update.UpdateID + 1
		}
		if len(updates) > 0 {
			if err := b.store.SaveUpdateOffset(ctx, offset); err != nil {
				b.logger.Error("save telegram update offset", "error", err)
			}
		}
		if cfg.Ready() && b.now().Sub(settled) >= settleInterval {
			settled = b.now()
			b.settle(ctx, client, cfg)
		}
	}
}

// money prints a ledger amount in the site's default currency.
func (b *Bot) money(minor int64) string { return b.settings.Current().Locale.Money(minor) }

func (b *Bot) budget(cfg settings.TelegramSettings) postgres.TelegramBudget {
	return postgres.TelegramBudget{DailyMinor: cfg.DailyBudgetMinor, Now: b.now()}
}

// settle pays the invitations that now meet the rules and tells the
// inviters.
func (b *Bot) settle(ctx context.Context, client API, cfg settings.TelegramSettings) {
	settled, err := b.store.SettleInvites(ctx, postgres.TelegramInviteRules{
		RewardMinor: cfg.InviteRewardMinor, Hold: cfg.InviteHold(), RequireLink: cfg.InviteRequireLink, DailyCap: cfg.InviteDailyCap,
	}, b.budget(cfg))
	if err != nil {
		b.logger.Error("settle telegram invites", "error", err)
		return
	}
	for _, item := range settled {
		link, err := b.store.LinkByAccount(ctx, item.InviterAccountID)
		if err != nil {
			continue
		}
		lang := b.settings.Current().Lang(link.Locale)
		_, _ = client.SendMessage(ctx, link.TelegramID, say(lang, msgInviteRewarded, html.EscapeString(item.InviteeName), b.money(item.RewardMinor)), 0)
	}
}

func (b *Bot) handle(ctx context.Context, client API, cfg settings.TelegramSettings, update Update) {
	switch {
	case update.MyChatMember != nil:
		b.botMembership(update.MyChatMember)
	case update.ChatMember != nil:
		b.membership(ctx, client, cfg, update.ChatMember)
	case update.Message != nil:
		b.message(ctx, client, cfg, update.Message)
	}
}

// botMembership remembers the groups the bot was added to.
func (b *Bot) botMembership(change *ChatMemberUpdated) {
	if change.Chat.Type != "group" && change.Chat.Type != "supergroup" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !change.NewChatMember.Present() {
		delete(b.chats, change.Chat.ID)
		return
	}
	b.chats[change.Chat.ID] = SeenChat{ID: change.Chat.ID, Title: change.Chat.Title, Username: change.Chat.Username, Admin: change.NewChatMember.Status == "administrator"}
}

// membership follows who joins and leaves the group. Joining through an
// account's invite link starts an invitation.
func (b *Bot) membership(ctx context.Context, client API, cfg settings.TelegramSettings, change *ChatMemberUpdated) {
	if !cfg.Ready() || change.Chat.ID != cfg.ChatID || change.NewChatMember.User.IsBot {
		return
	}
	user := change.NewChatMember.User
	was, is := change.OldChatMember.Present(), change.NewChatMember.Present()
	switch {
	case !was && is:
		link := ""
		if change.InviteLink != nil {
			link = change.InviteLink.InviteLink
		}
		if _, err := b.store.MemberJoined(ctx, postgres.TelegramUser{ID: user.ID, Username: user.Username, FirstName: user.FirstName}, link); err != nil {
			b.logger.Error("record telegram member", "error", err)
			return
		}
		b.remember(user.ID)
		if cfg.Welcome {
			site := b.settings.Current().PublicURL
			b.groupReply(ctx, client, cfg, 0, say(b.langOf(ctx, user), msgWelcome, mention(user), site+"/portal/profile", siteHost(site)))
		}
	case was && !is:
		if err := b.store.MemberLeft(ctx, user.ID); err != nil {
			b.logger.Error("record telegram member leaving", "error", err)
		}
		b.forget(user.ID)
	}
}

func (b *Bot) remember(id int64) {
	b.mu.Lock()
	b.members[id] = struct{}{}
	b.mu.Unlock()
}

func (b *Bot) forget(id int64) {
	b.mu.Lock()
	delete(b.members, id)
	b.mu.Unlock()
}

func (b *Bot) known(id int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.members[id]
	return ok
}

// langOf is the language to answer a Telegram user in: the one their site
// account reads, else Chinese for Chinese Telegram clients and English
// for the rest.
func (b *Bot) langOf(ctx context.Context, user User) string {
	if link, err := b.store.LinkByTelegram(ctx, user.ID); err == nil && link.Locale != "" {
		return link.Locale
	}
	if strings.HasPrefix(strings.ToLower(user.LanguageCode), "zh") {
		return "zh"
	}
	if user.LanguageCode == "" {
		return b.settings.Current().Lang("")
	}
	return "en"
}

// command splits a message into a command and its argument. Commands
// addressed to another bot are not ours.
func command(text, botUsername string) (string, string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	word := fields[0]
	argument := strings.Join(fields[1:], " ")
	if strings.HasPrefix(word, "/") {
		name, target, addressed := strings.Cut(strings.TrimPrefix(word, "/"), "@")
		if addressed && !strings.EqualFold(target, botUsername) {
			return "", ""
		}
		return strings.ToLower(name), argument
	}
	if len(fields) > 1 {
		return "", ""
	}
	switch word {
	case "签到", "打卡":
		return "checkin", ""
	case "邀请", "邀请链接":
		return "invite", ""
	}
	return "", ""
}

func (b *Bot) message(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message) {
	if message.From == nil || message.From.IsBot {
		return
	}
	user := *message.From
	private := message.Chat.Type == "private"
	inGroup := cfg.ChatID != 0 && message.Chat.ID == cfg.ChatID
	if !private && !inGroup {
		return
	}
	if inGroup && cfg.Ready() {
		if message.LeftChatMember != nil && !message.LeftChatMember.IsBot {
			_ = b.store.MemberLeft(ctx, message.LeftChatMember.ID)
			b.forget(message.LeftChatMember.ID)
		}
		// Whoever talks in the group is a member already, so leaving and
		// coming back through an invite link is not a new member. Service
		// messages about joining are left to the membership updates, which
		// carry the invite link.
		if len(message.NewChatMembers) == 0 && message.LeftChatMember == nil && !b.known(user.ID) {
			if _, err := b.store.MemberJoined(ctx, postgres.TelegramUser{ID: user.ID, Username: user.Username, FirstName: user.FirstName}, ""); err == nil {
				b.remember(user.ID)
			}
		}
	}
	name, argument := command(message.Text, cfg.BotUsername)
	if name == "" {
		return
	}
	lang := b.langOf(ctx, user)
	answer := func(text string) {
		if private {
			_, _ = client.SendMessage(ctx, message.Chat.ID, text, 0)
			return
		}
		b.groupReply(ctx, client, cfg, message.MessageID, text)
	}
	if !cfg.Ready() {
		if private {
			answer(say(lang, msgDisabled))
		}
		return
	}
	switch name {
	case "start":
		if code, ok := strings.CutPrefix(argument, "bind_"); ok && private {
			answer(b.bind(ctx, cfg, user, code, lang))
			return
		}
		answer(b.help(cfg, lang))
	case "help":
		answer(b.help(cfg, lang))
	case "checkin", "qd", "sign":
		if private {
			answer(say(lang, msgCheckinInGroup, b.groupLine(cfg, lang)))
			return
		}
		answer(b.checkin(ctx, cfg, user, lang))
	case "invite":
		b.personal(ctx, client, cfg, message, lang, b.invite(ctx, client, cfg, user, lang))
	case "me", "balance":
		b.personal(ctx, client, cfg, message, lang, b.me(ctx, user, lang))
	}
}

// personal answers with something only the sender should read: in a
// private chat directly; from the group, by private message.
func (b *Bot) personal(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message, lang, text string) {
	if message.Chat.Type == "private" {
		_, _ = client.SendMessage(ctx, message.Chat.ID, text, 0)
		return
	}
	if _, err := client.SendMessage(ctx, message.From.ID, text, 0); err != nil {
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgStartBotFirst, cfg.BotUsername))
		return
	}
	b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgSentPrivately))
}

// groupReply posts in the group and removes the post after the configured
// time, so the bot does not flood the conversation.
func (b *Bot) groupReply(ctx context.Context, client API, cfg settings.TelegramSettings, replyTo int64, text string) {
	sent, err := client.SendMessage(ctx, cfg.ChatID, text, replyTo)
	if err != nil {
		b.logger.Warn("telegram group reply", "error", err)
		return
	}
	if cfg.ReplyTTLSeconds <= 0 {
		return
	}
	go func() {
		sleep(ctx, time.Duration(cfg.ReplyTTLSeconds)*time.Second)
		if ctx.Err() == nil {
			_ = client.DeleteMessage(ctx, cfg.ChatID, sent.MessageID)
		}
	}()
}

func (b *Bot) groupLine(cfg settings.TelegramSettings, lang string) string {
	if cfg.ChatURL == "" {
		return ""
	}
	return say(lang, msgGroupLink, html.EscapeString(cfg.ChatURL))
}

func (b *Bot) help(cfg settings.TelegramSettings, lang string) string {
	site := b.settings.Current()
	bindNote, inviteNote := "", ""
	if cfg.BindRewardMinor > 0 {
		bindNote = say(lang, msgHelpBind, b.money(cfg.BindRewardMinor))
	}
	if cfg.InviteRewardMinor > 0 {
		inviteNote = say(lang, msgHelpInvite, b.money(cfg.InviteRewardMinor))
	}
	return say(lang, msgHelp, html.EscapeString(site.AppName), bindNote, amountRange(cfg, b.money), inviteNote, site.PublicURL, siteHost(site.PublicURL))
}

func (b *Bot) notLinked(lang string) string {
	site := b.settings.Current().PublicURL
	return say(lang, msgNotLinked, site+"/portal/profile", siteHost(site))
}

func (b *Bot) bind(ctx context.Context, cfg settings.TelegramSettings, user User, code, lang string) string {
	result, err := b.store.Bind(ctx, security.HashToken(strings.TrimSpace(code)), postgres.TelegramUser{ID: user.ID, Username: user.Username, FirstName: user.FirstName}, cfg.BindRewardMinor, b.budget(cfg))
	switch {
	case errors.Is(err, postgres.ErrTelegramCodeInvalid):
		return say(lang, msgBindInvalid)
	case errors.Is(err, postgres.ErrTelegramTaken):
		return say(lang, msgBindTaken)
	case errors.Is(err, postgres.ErrTelegramAlreadyLinked):
		return say(lang, msgBindAlready)
	case errors.Is(err, postgres.ErrEmailUnverified):
		return say(lang, msgBindUnverified)
	case err != nil:
		b.logger.Error("link telegram account", "error", err)
		return say(lang, msgBindInvalid)
	}
	// The site account's own language from here on.
	lang = b.settings.Current().Lang(firstNonEmpty(result.Link.Locale, lang))
	text := say(lang, msgBound, maskEmail(result.Link.Email))
	if result.BonusMinor > 0 {
		text += say(lang, msgBoundBonus, b.money(result.BonusMinor), b.money(result.BalanceMinor))
	}
	return text + "\n\n" + b.help(cfg, lang)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (b *Bot) checkin(ctx context.Context, cfg settings.TelegramSettings, user User, lang string) string {
	link, err := b.store.LinkByTelegram(ctx, user.ID)
	if errors.Is(err, postgres.ErrTelegramNotLinked) {
		return b.notLinked(lang)
	}
	if err != nil {
		b.logger.Error("telegram check-in", "error", err)
		return ""
	}
	result, err := b.store.Checkin(ctx, link, b.between(cfg.CheckinMinMinor, cfg.CheckinMaxMinor), b.budget(cfg))
	if err != nil {
		b.logger.Error("telegram check-in", "error", err)
		return ""
	}
	switch {
	case result.Already:
		return say(lang, msgCheckinAlready, mention(user), result.Streak)
	case result.Exhausted:
		return say(lang, msgCheckinExhausted)
	}
	return say(lang, msgCheckinDone, mention(user), b.money(result.AmountMinor), b.money(result.BalanceMinor), result.Streak)
}

// InviteLink is the account's own invite link to the group, made on
// first use.
func (b *Bot) InviteLink(ctx context.Context, accountID string) (string, error) {
	cfg := b.settings.Current().TelegramLedger()
	if !cfg.Ready() || cfg.InviteRewardMinor <= 0 {
		return "", ErrNotReady
	}
	return b.inviteLink(ctx, b.client(cfg), cfg, accountID)
}

func (b *Bot) inviteLink(ctx context.Context, client API, cfg settings.TelegramSettings, accountID string) (string, error) {
	b.inviteMu.Lock()
	defer b.inviteMu.Unlock()
	link, err := b.store.InviteLink(ctx, accountID, cfg.ChatID)
	if err != nil || link != "" {
		return link, err
	}
	name := "vpsbill-" + accountID
	if len(name) > 32 {
		name = name[:32]
	}
	if link, err = client.CreateInviteLink(ctx, cfg.ChatID, name); err != nil {
		return "", err
	}
	return link, b.store.SaveInviteLink(ctx, accountID, cfg.ChatID, link)
}

func (b *Bot) invite(ctx context.Context, client API, cfg settings.TelegramSettings, user User, lang string) string {
	if cfg.InviteRewardMinor <= 0 {
		return say(lang, msgInviteOff)
	}
	link, err := b.store.LinkByTelegram(ctx, user.ID)
	if errors.Is(err, postgres.ErrTelegramNotLinked) {
		return b.notLinked(lang)
	}
	if err != nil {
		return say(lang, msgInviteUnavailable)
	}
	url, err := b.inviteLink(ctx, client, cfg, link.AccountID)
	if err != nil {
		b.logger.Warn("make telegram invite link", "error", err)
		return say(lang, msgInviteUnavailable)
	}
	stats, _ := b.store.AccountStats(ctx, link.AccountID, b.now())
	conditions := ""
	if cfg.InviteRequireLink {
		conditions += say(lang, msgInviteLinked)
	}
	if cfg.InviteHoldHours > 0 {
		conditions += say(lang, msgInviteHold, cfg.InviteHoldHours)
	}
	text := say(lang, msgInvite, html.EscapeString(url), conditions, b.money(cfg.InviteRewardMinor), stats.InvitesRewarded, stats.InvitesPending)
	if cfg.InviteDailyCap > 0 {
		text += say(lang, msgInviteCap, cfg.InviteDailyCap)
	}
	return text
}

func (b *Bot) me(ctx context.Context, user User, lang string) string {
	link, err := b.store.LinkByTelegram(ctx, user.ID)
	if err != nil {
		return b.notLinked(lang)
	}
	stats, err := b.store.AccountStats(ctx, link.AccountID, b.now())
	if err != nil {
		return ""
	}
	balance, _ := b.store.Balance(ctx, link.AccountID)
	today := ""
	if stats.CheckedInToday {
		today = say(lang, msgMeToday)
	}
	return say(lang, msgMe, maskEmail(link.Email), b.money(balance), stats.Checkins, today, stats.InvitesRewarded, stats.InvitesPending, b.money(stats.EarnedMinor))
}

// Check is what Telegram says about a bot and the group it is set up for.
type Check struct {
	BotUsername string `json:"bot_username"`
	ChatID      int64  `json:"chat_id"`
	ChatTitle   string `json:"chat_title"`
	ChatURL     string `json:"chat_url"`
	// Warnings are things that work less well than they could.
	Warnings []string `json:"warnings"`
}

// Verify asks Telegram who the bot is and, when a chat is given (a
// numeric id or an @username), whether the bot can work there.
func (b *Bot) Verify(ctx context.Context, apiBase, token, chat string) (Check, error) {
	client := b.connect(apiBase, token)
	me, err := client.GetMe(ctx)
	if err != nil {
		return Check{}, err
	}
	check := Check{BotUsername: me.Username, Warnings: []string{}}
	chat = strings.TrimSpace(chat)
	if chat == "" {
		return check, nil
	}
	var target any = chat
	var id int64
	if _, scanErr := fmt.Sscan(chat, &id); scanErr == nil && fmt.Sprint(id) == chat {
		target = id
	} else if !strings.HasPrefix(chat, "@") {
		// A t.me/name link or a bare name.
		target = "@" + strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(chat, "https://"), "http://"), "t.me/")
	}
	found, err := client.GetChat(ctx, target)
	if err != nil {
		return Check{}, fmt.Errorf("找不到这个群：%w", err)
	}
	if found.Type != "group" && found.Type != "supergroup" {
		return Check{}, errors.New("这不是一个群（频道和私聊不能用于签到）")
	}
	check.ChatID, check.ChatTitle = found.ID, found.Title
	if found.Username != "" {
		check.ChatURL = "https://t.me/" + found.Username
	}
	member, err := client.GetChatMember(ctx, found.ID, me.ID)
	switch {
	case err != nil || !member.Present():
		return Check{}, errors.New("机器人还不在这个群里，请先把它拉进群")
	case member.Status != "administrator":
		check.Warnings = append(check.Warnings, "机器人不是群管理员：收不到成员进群和退群的消息，邀请奖励无法结算，也不能生成专属邀请链接。")
	case !member.CanInviteUsers:
		check.Warnings = append(check.Warnings, "机器人没有「邀请用户」权限，不能生成专属邀请链接。")
	}
	if member.Status == "administrator" && !member.CanDeleteMessage {
		check.Warnings = append(check.Warnings, "机器人没有「删除消息」权限，群内回复不会自动清理。")
	}
	return check, nil
}
