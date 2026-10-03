package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
)

// fakeAPI stands in for Telegram: it keeps what the bot sent.
type fakeAPI struct {
	mu      sync.Mutex
	sent    []sentMessage
	edits   []string
	answers []string
	links   int
	noLinks bool
	// What the group's moderation did.
	restricted map[int64]bool
	kicked     []int64
	deleted    []int64
	admins     []ChatMember
}

func (f *fakeAPI) RestrictMember(_ context.Context, _, userID int64, allow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restricted == nil {
		f.restricted = map[int64]bool{}
	}
	f.restricted[userID] = allow
	return nil
}

func (f *fakeAPI) KickMember(_ context.Context, _, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kicked = append(f.kicked, userID)
	return nil
}

func (f *fakeAPI) ChatAdministrators(context.Context, int64) ([]ChatMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admins, nil
}

// pngImage is the start of a PNG file, which is enough to be sniffed as one.
var pngImage = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

func (f *fakeAPI) Download(context.Context, string, int64) ([]byte, error) { return pngImage, nil }

type sentMessage struct {
	chat    int64
	text    string
	buttons [][]Button
}

func (f *fakeAPI) SendButtons(_ context.Context, chatID int64, html string, buttons [][]Button) (Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID, html, buttons})
	return Message{MessageID: int64(len(f.sent))}, nil
}

func (f *fakeAPI) EditMessage(_ context.Context, _, _ int64, html string, _ [][]Button) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, html)
	return nil
}

func (f *fakeAPI) AnswerCallback(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	return nil
}

// lastButtons are the buttons under the newest message sent to chat.
func (f *fakeAPI) lastButtons(chat int64) [][]Button {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].chat == chat {
			return f.sent[i].buttons
		}
	}
	return nil
}

func (f *fakeAPI) lastEdit() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.edits) == 0 {
		return ""
	}
	return f.edits[len(f.edits)-1]
}

func (f *fakeAPI) lastAnswer() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.answers) == 0 {
		return ""
	}
	return f.answers[len(f.answers)-1]
}

func (f *fakeAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, IsBot: true, Username: "site_bot"}, nil
}
func (f *fakeAPI) GetUpdates(context.Context, int64) ([]Update, error) { return nil, nil }
func (f *fakeAPI) SendMessage(_ context.Context, chatID int64, html string, _ int64) (Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID, html, nil})
	return Message{MessageID: int64(len(f.sent))}, nil
}
func (f *fakeAPI) DeleteMessage(_ context.Context, _, messageID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, messageID)
	return nil
}
func (f *fakeAPI) GetChat(context.Context, any) (Chat, error) {
	return Chat{ID: groupID, Type: "supergroup", Title: "Site Group", Username: "sitegroup"}, nil
}
func (f *fakeAPI) GetChatMember(context.Context, int64, int64) (ChatMember, error) {
	return ChatMember{Status: "administrator", CanInviteUsers: true, CanDeleteMessage: true, CanRestrictMembers: true}, nil
}
func (f *fakeAPI) CreateInviteLink(_ context.Context, _ int64, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noLinks {
		return "", &APIError{Code: 400, Description: "not enough rights"}
	}
	f.links++
	return fmt.Sprintf("https://t.me/+invite%d", f.links), nil
}

// last is the newest message sent to chat.
func (f *fakeAPI) last(t *testing.T, chat int64) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].chat == chat {
			return f.sent[i].text
		}
	}
	t.Fatalf("nothing was sent to chat %d", chat)
	return ""
}

const groupID = int64(-1001234567890)

func TestBotIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "telegram")
	box, err := security.NewSecretBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := settings.NewManager(ctx, db, box, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := runtime.Install(ctx, settings.InstallInput{AppName: "Test Cloud", PublicURL: "https://cloud.example.com", AdminDisplayName: "Admin", AdminEmail: "admin@example.com", AdminPasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	admin := installed.Identity.UserID
	cfg := settings.DefaultTelegramSettings()
	cfg.Enabled, cfg.BotToken, cfg.BotUsername, cfg.ChatID, cfg.ChatURL = true, "123:token", "site_bot", groupID, "https://t.me/sitegroup"
	cfg.BindRewardMinor, cfg.CheckinMinMinor, cfg.CheckinMaxMinor, cfg.InviteRewardMinor = 100, 10, 50, 200
	cfg.ReplyTTLSeconds, cfg.DailyBudgetMinor = 0, 0
	if err = runtime.SetTelegram(ctx, cfg, admin); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Current().Telegram; !got.Ready() || got.BotToken != "123:token" {
		t.Fatalf("settings did not round-trip: %+v", got)
	}

	store := postgres.NewTelegramStore(db)
	api := &fakeAPI{}
	now := time.Now()
	bot := New(store, runtime, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bot.connect = func(string, string) API { return api }
	bot.now = func() time.Time { return now }
	current := func() settings.TelegramSettings { return runtime.Current().Telegram }

	alice := newCustomer(t, ctx, db, "alice@example.com", "zh", true)
	bob := newCustomer(t, ctx, db, "bob@example.com", "en", true)
	carol := newCustomer(t, ctx, db, "carol@example.com", "", false)

	private := func(user User, text string) {
		bot.handle(ctx, api, current(), Update{Message: &Message{MessageID: 9, From: &user, Chat: Chat{ID: user.ID, Type: "private"}, Text: text}})
	}
	group := func(user User, text string) {
		bot.handle(ctx, api, current(), Update{Message: &Message{MessageID: 9, From: &user, Chat: Chat{ID: groupID, Type: "supergroup"}, Text: text}})
	}
	join := func(user User, link string) {
		change := &ChatMemberUpdated{Chat: Chat{ID: groupID, Type: "supergroup"}, OldChatMember: ChatMember{Status: "left", User: user}, NewChatMember: ChatMember{Status: "member", User: user}}
		if link != "" {
			change.InviteLink = &struct {
				InviteLink string `json:"invite_link"`
			}{link}
		}
		bot.handle(ctx, api, current(), Update{ChatMember: change})
	}
	leave := func(user User) {
		bot.handle(ctx, api, current(), Update{ChatMember: &ChatMemberUpdated{Chat: Chat{ID: groupID, Type: "supergroup"}, OldChatMember: ChatMember{Status: "member", User: user}, NewChatMember: ChatMember{Status: "left", User: user}}})
	}
	bindCode := func(userID string) string {
		code := fmt.Sprintf("code%d", time.Now().UnixNano())
		if err := store.CreateBindCode(ctx, userID, security.HashToken(code), time.Now().Add(BindCodeTTL)); err != nil {
			t.Fatalf("create bind code: %v", err)
		}
		return code
	}
	balance := func(c customer) int64 {
		value, err := store.Balance(ctx, c.account)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	tgAlice := User{ID: 1001, FirstName: "Alice <A>", Username: "alice", LanguageCode: "zh-hans"}
	tgBob := User{ID: 1002, FirstName: "Bob", LanguageCode: "en"}
	tgEve := User{ID: 1003, FirstName: "Eve", LanguageCode: "ru"}

	// Checking in without a linked account points to the site.
	group(tgAlice, "签到")
	if text := api.last(t, groupID); !strings.Contains(text, "还没有绑定") || !strings.Contains(text, "cloud.example.com/portal/profile") {
		t.Fatalf("unlinked check-in answer: %s", text)
	}

	// An unverified address cannot link.
	if err := store.CreateBindCode(ctx, carol.user, security.HashToken("x"), time.Now().Add(time.Minute)); err != postgres.ErrEmailUnverified {
		t.Fatalf("unverified bind code: %v", err)
	}

	// Linking pays the bonus once and a code works once.
	code := bindCode(alice.user)
	private(tgAlice, "/start bind_"+code)
	if text := api.last(t, tgAlice.ID); !strings.Contains(text, "绑定成功") || !strings.Contains(text, "al***@example.com") || !strings.Contains(text, "¥1.00") {
		t.Fatalf("bind answer: %s", text)
	}
	if got := balance(alice); got != 100 {
		t.Fatalf("balance after linking = %d, want 100", got)
	}
	private(tgBob, "/start bind_"+code)
	if text := api.last(t, tgBob.ID); !strings.Contains(text, "invalid or has expired") {
		t.Fatalf("reused code answer: %s", text)
	}
	if err := store.CreateBindCode(ctx, alice.user, security.HashToken("y"), time.Now().Add(time.Minute)); err != postgres.ErrTelegramAlreadyLinked {
		t.Fatalf("second bind code: %v", err)
	}

	// One check-in a day, within the configured range.
	group(tgAlice, "签到")
	text := api.last(t, groupID)
	earned := balance(alice) - 100
	if !strings.Contains(text, "签到成功") || !strings.Contains(text, "Alice &lt;A&gt;") || earned < 10 || earned > 50 {
		t.Fatalf("check-in: earned %d, answer %s", earned, text)
	}
	group(tgAlice, "/checkin@site_bot")
	if text := api.last(t, groupID); !strings.Contains(text, "今天已经签到过了") || balance(alice) != 100+earned {
		t.Fatalf("second check-in: %s balance %d", text, balance(alice))
	}
	group(tgAlice, "/checkin@other_bot")
	now = now.Add(24 * time.Hour)
	group(tgAlice, "打卡")
	if text := api.last(t, groupID); !strings.Contains(text, "已连续签到 2 天") {
		t.Fatalf("second day check-in: %s", text)
	}
	afterCheckins := balance(alice)

	// The invite link is made once and sent privately.
	group(tgAlice, "/invite")
	link := "https://t.me/+invite1"
	if text := api.last(t, tgAlice.ID); !strings.Contains(text, link) || !strings.Contains(text, "¥2.00") {
		t.Fatalf("invite answer: %s", text)
	}
	if text := api.last(t, groupID); !strings.Contains(text, "已私聊") {
		t.Fatalf("invite group answer: %s", text)
	}
	private(tgAlice, "/invite")
	if api.links != 1 {
		t.Fatalf("invite link made %d times", api.links)
	}

	// Bob joins through Alice's link. Nothing is paid until he stayed
	// long enough and linked a verified account.
	join(tgBob, link)
	if text := api.last(t, groupID); !strings.Contains(text, "Welcome, Bob") {
		t.Fatalf("welcome: %s", text)
	}
	bot.settle(ctx, api, current())
	now = now.Add(25 * time.Hour)
	bot.settle(ctx, api, current())
	if balance(alice) != afterCheckins {
		t.Fatal("invitation was paid before the invitee linked an account")
	}
	private(tgBob, "/start bind_"+bindCode(bob.user))
	if text := api.last(t, tgBob.ID); !strings.Contains(text, "Linked!") {
		t.Fatalf("bob's bind answer: %s", text)
	}
	bot.settle(ctx, api, current())
	if got := balance(alice); got != afterCheckins+200 {
		t.Fatalf("balance after invitation = %d, want %d", got, afterCheckins+200)
	}
	if text := api.last(t, tgAlice.ID); !strings.Contains(text, "你邀请的 Bob") {
		t.Fatalf("inviter notice: %s", text)
	}
	bot.settle(ctx, api, current())
	if got := balance(alice); got != afterCheckins+200 {
		t.Fatalf("invitation was paid twice: %d", got)
	}

	// Leaving voids a pending invitation, and coming back through the
	// link is not a new member.
	join(tgEve, link)
	leave(tgEve)
	join(tgEve, link)
	var status, reason string
	if err := db.QueryRow(ctx, `SELECT status,void_reason FROM telegram_invites WHERE invitee_telegram_id=$1`, tgEve.ID).Scan(&status, &reason); err != nil || status != "void" || reason != "left" {
		t.Fatalf("eve's invitation: %s %s %v", status, reason, err)
	}
	// Someone who talked in the group before is not a new member either.
	old := User{ID: 1004, FirstName: "Old"}
	group(old, "hello")
	leave(old)
	join(old, link)
	var invitations int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM telegram_invites WHERE invitee_telegram_id=$1`, old.ID).Scan(&invitations); err != nil || invitations != 0 {
		t.Fatalf("returning member started %d invitations (%v)", invitations, err)
	}

	// English for accounts that read the site in English.
	group(tgBob, "/checkin")
	if text := api.last(t, groupID); !strings.Contains(text, "checked in") {
		t.Fatalf("english check-in: %s", text)
	}
	private(tgBob, "/me")
	if text := api.last(t, tgBob.ID); !strings.Contains(text, "Site account: bo***@example.com") {
		t.Fatalf("/me: %s", text)
	}
	private(tgBob, "/help")
	if text := api.last(t, tgBob.ID); !strings.Contains(text, "Test Cloud") || !strings.Contains(text, "¥0.10–¥0.50") {
		t.Fatalf("/help: %s", text)
	}

	// The daily budget stops rewards once it is spent.
	cfg = current()
	cfg.DailyBudgetMinor = 1
	if err = runtime.SetTelegram(ctx, cfg, admin); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	before := balance(alice)
	group(tgAlice, "签到")
	if text := api.last(t, groupID); !strings.Contains(text, "已经发完") || balance(alice) != before {
		t.Fatalf("budget: %s balance %d (was %d)", text, balance(alice), before)
	}

	// A link has to stand a week before it can be removed; the Telegram
	// account is free for another site account afterwards, without a
	// second bonus.
	if err := store.Unlink(ctx, alice.user, time.Now()); err != postgres.ErrTelegramCooldown {
		t.Fatalf("early unlink: %v", err)
	}
	if err := store.Unlink(ctx, alice.user, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	cfg.DailyBudgetMinor = 0
	if err = runtime.SetTelegram(ctx, cfg, admin); err != nil {
		t.Fatal(err)
	}
	dave := newCustomer(t, ctx, db, "dave@example.com", "zh", true)
	private(tgAlice, "/start bind_"+bindCode(dave.user))
	if text := api.last(t, tgAlice.ID); !strings.Contains(text, "绑定成功") || strings.Contains(text, "绑定奖励") || balance(dave) != 0 {
		t.Fatalf("relinked telegram account: %s balance %d", text, balance(dave))
	}
	// The day's check-in was used by neither account, but the Telegram
	// account may only check in once whatever it is linked to.
	group(tgAlice, "签到")
	if balance(dave) == 0 {
		t.Fatalf("check-in after relinking paid nothing: %s", api.last(t, groupID))
	}
	private(tgAlice, "/start bind_"+bindCode(alice.user))
	if text := api.last(t, tgAlice.ID); !strings.Contains(text, "已经绑定了另一个站点账号") {
		t.Fatalf("taken telegram account: %s", text)
	}

	// Without the right to invite, the bot says so instead of failing.
	api.noLinks = true
	private(tgBob, "/invite")
	if text := api.last(t, tgBob.ID); !strings.Contains(text, "cannot be made right now") {
		t.Fatalf("invite without rights: %s", text)
	}

	// The settings page's check.
	check, err := bot.Verify(ctx, "", "123:token", "@sitegroup")
	if err != nil || check.BotUsername != "site_bot" || check.ChatID != groupID || check.ChatURL != "https://t.me/sitegroup" || len(check.Warnings) != 0 {
		t.Fatalf("verify: %+v %v", check, err)
	}

	stats, err := store.SiteStats(ctx, now)
	if err != nil || stats.Links != 2 || stats.InvitesRewarded != 1 || stats.RewardedTotal <= 300 {
		t.Fatalf("site stats: %+v %v", stats, err)
	}
	// Every reward is in the ledger as a reward entry.
	var ledger, balances int64
	if err := db.QueryRow(ctx, `SELECT (SELECT coalesce(sum(amount_minor),0) FROM wallet_entries WHERE kind='reward'),(SELECT coalesce(sum(balance_minor),0) FROM accounts)`).Scan(&ledger, &balances); err != nil || ledger != balances {
		t.Fatalf("ledger %d does not match balances %d (%v)", ledger, balances, err)
	}
}

type customer struct{ user, account string }

func newCustomer(t *testing.T, ctx context.Context, db *pgxpool.Pool, email, locale string, verified bool) customer {
	t.Helper()
	var c customer
	if err := db.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,email_verified_at,status,locale) VALUES($1,$1,'x',CASE WHEN $3::boolean THEN now() END,'active',$2) RETURNING id`, email, locale, verified).Scan(&c.user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO accounts(kind,status,display_name,billing_email,country_code,default_currency) VALUES('individual','active',$1,$1,'CN','CNY') RETURNING id`, email).Scan(&c.account); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO memberships(account_id,user_id,role) VALUES($1,$2,'owner')`, c.account, c.user); err != nil {
		t.Fatal(err)
	}
	return c
}
