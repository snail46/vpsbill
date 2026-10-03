package telegram

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
)

// ticketEvents records what the bot tells about tickets.
type ticketEvents struct {
	mu      sync.Mutex
	created []string
	replied []string
}

func (e *ticketEvents) TicketCreated(_ context.Context, ticketID, _, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.created = append(e.created, ticketID+":"+body)
}

func (e *ticketEvents) TicketReplied(_ context.Context, ticketID, _, authorType, body string, _ bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.replied = append(e.replied, authorType+":"+body)
}

// wait gives the background events a moment.
func (e *ticketEvents) wait(t *testing.T, created, replied int) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		e.mu.Lock()
		done := len(e.created) >= created && len(e.replied) >= replied
		e.mu.Unlock()
		if done {
			return
		}
	}
	t.Fatalf("ticket events: created %v replied %v", e.created, e.replied)
}

const staffChatID = int64(-1009999)

// The second stage: tickets on Telegram, red packets, streak bonuses,
// leaderboards, the new members' check and the group's filters.
func TestBotPhaseTwoIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "telegramstagetwo")
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
	// A Wednesday (UTC+8), so that the week's board has room behind it.
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, clock.Zone)
	cfg := settings.DefaultTelegramSettings()
	cfg.Enabled, cfg.BotToken, cfg.BotUsername, cfg.ChatID, cfg.AdminChatID = true, "123:token", "site_bot", groupID, staffChatID
	cfg.CheckinMinMinor, cfg.CheckinMaxMinor, cfg.ReplyTTLSeconds, cfg.DailyBudgetMinor = 30, 30, 0, 0
	cfg.Tickets, cfg.StreakBonuses = true, []settings.StreakBonus{{Days: 3, AmountMinor: 70}, {Days: 30, AmountMinor: 900}}
	cfg.Leaderboards, cfg.LeaderboardPrizes = true, []int64{500, 300}
	cfg.Verify, cfg.VerifyMinutes = settings.VerifyButton, 10
	cfg.FilterLinks, cfg.BlockedWords, cfg.UnlinkedPerMinute = true, []string{"赌博"}, 3
	if err := runtime.SetTelegram(ctx, cfg, admin); err != nil {
		t.Fatal(err)
	}
	// Lists are validated: no streak of one day.
	bad := cfg
	bad.StreakBonuses = []settings.StreakBonus{{Days: 1, AmountMinor: 10}}
	if err := runtime.SetTelegram(ctx, bad, admin); err == nil {
		t.Fatal("a one-day streak was accepted")
	}

	store := postgres.NewTelegramStore(db)
	api := &fakeAPI{admins: []ChatMember{{Status: "administrator", User: User{ID: 777}}}}
	events := &ticketEvents{}
	bot := New(store, runtime, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bot.connect = func(string, string) API { return api }
	bot.now = func() time.Time { return now }
	bot.SetTicketEvents(events)
	current := func() settings.TelegramSettings { return runtime.Current().TelegramLedger() }

	alice := newCustomer(t, ctx, db, "alice@example.com", "zh", true)
	bob := newCustomer(t, ctx, db, "bob@example.com", "zh", true)
	carol := newCustomer(t, ctx, db, "carol@example.com", "zh", true)
	aliceTG, bobTG, carolTG := User{ID: 501, FirstName: "Alice"}, User{ID: 502, FirstName: "Bob"}, User{ID: 503, FirstName: "Carol"}
	eveTG, daveTG, ownerTG := User{ID: 601, FirstName: "Eve"}, User{ID: 602, FirstName: "Dave"}, User{ID: 777, FirstName: "Owner"}
	adminTG, readerTG := User{ID: 900, FirstName: "Admin"}, User{ID: 901, FirstName: "Reader"}
	for _, link := range []struct {
		c    customer
		user User
	}{{alice, aliceTG}, {bob, bobTG}, {carol, carolTG}} {
		if _, err := db.Exec(ctx, `INSERT INTO telegram_links(user_id,account_id,telegram_id,first_name,linked_at) VALUES($1,$2,$3,$4,$5)`, link.c.user, link.c.account, link.user.ID, link.user.FirstName, now.Add(-10*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// A read-only staff member, linked.
	var reader string
	if err := db.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,email_verified_at,status) VALUES('reader@example.com','Reader','x',now(),'active') RETURNING id`).Scan(&reader); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO staff_members(user_id,role_id) SELECT $1,id FROM staff_roles WHERE name='Read Only'`, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO telegram_staff_links(user_id,telegram_id) VALUES($1,$2)`, reader, readerTG.ID); err != nil {
		t.Fatal(err)
	}

	nextID := int64(1000)
	send := func(chat Chat, user User, text string, replyTo *Message) int64 {
		nextID++
		bot.handle(ctx, api, current(), Update{Message: &Message{MessageID: nextID, From: &user, Chat: chat, Text: text, ReplyTo: replyTo}})
		return nextID
	}
	private := func(user User, text string) { send(Chat{ID: user.ID, Type: "private"}, user, text, nil) }
	group := func(user User, text string) int64 {
		return send(Chat{ID: groupID, Type: "supergroup"}, user, text, nil)
	}
	botMessage := func(id int64) *Message {
		return &Message{MessageID: id, From: &User{ID: 1, IsBot: true, Username: "site_bot"}}
	}
	press := func(user User, chat int64, data string) {
		bot.handle(ctx, api, current(), Update{Callback: &CallbackQuery{ID: "q", From: user, Data: data, Message: &Message{MessageID: 55, Chat: Chat{ID: chat, Type: "supergroup"}, Text: "Ticket notice"}}})
	}
	lastSentID := func() int64 {
		api.mu.Lock()
		defer api.mu.Unlock()
		return int64(len(api.sent))
	}
	count := func(query string, args ...any) (value int64) {
		t.Helper()
		if err := db.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	balance := func(c customer) int64 {
		value, err := store.Balance(ctx, c.account)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	// ---- Staff link their Telegram account with a code ----
	if err := store.CreateStaffBindCode(ctx, admin, security.HashToken("staffcode"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateStaffBindCode(ctx, alice.user, security.HashToken("nope"), time.Now().Add(time.Minute)); err != postgres.ErrNotStaff {
		t.Fatalf("a customer made a staff code: %v", err)
	}
	private(adminTG, "/start staff_staffcode")
	if text := api.last(t, adminTG.ID); !strings.Contains(text, "已绑定管理员账号") || !strings.Contains(text, "/redpacket") {
		t.Fatalf("staff link: %s", text)
	}

	// ---- Tickets ----
	private(aliceTG, "/ticket")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "/ticket 主题") {
		t.Fatalf("ticket usage: %s", text)
	}
	private(aliceTG, "/ticket 无法连接 SSH\n从早上开始连不上")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "已创建工单") || !strings.Contains(text, "无法连接 SSH") {
		t.Fatalf("ticket opened: %s", text)
	}
	confirmation := lastSentID()
	var ticketID string
	if err := db.QueryRow(ctx, `SELECT id FROM support_tickets WHERE account_id=$1 AND subject='无法连接 SSH'`, alice.account).Scan(&ticketID); err != nil {
		t.Fatalf("the ticket was not saved: %v", err)
	}
	if body := count(`SELECT count(*) FROM support_messages WHERE ticket_id=$1 AND body='从早上开始连不上' AND author_type='customer'`, ticketID); body != 1 {
		t.Fatal("the ticket's body was not saved")
	}
	events.wait(t, 1, 0)

	// Replying to the confirmation adds to the ticket, with an image.
	nextID++
	bot.handle(ctx, api, current(), Update{Message: &Message{MessageID: nextID, From: &aliceTG, Chat: Chat{ID: aliceTG.ID, Type: "private"}, Caption: "重启也不行", Photo: []PhotoSize{{FileID: "small"}, {FileID: "large"}}, ReplyTo: botMessage(confirmation)}})
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "已回复工单") {
		t.Fatalf("customer reply: %s", text)
	}
	if n := count(`SELECT count(*) FROM support_attachments WHERE ticket_id=$1 AND content_type='image/png'`, ticketID); n != 1 {
		t.Fatalf("attachments = %d", n)
	}
	events.wait(t, 1, 1)
	// Someone else cannot answer it; a reply to a message about nothing is refused.
	send(Chat{ID: bobTG.ID, Type: "private"}, bobTG, "我也来", botMessage(confirmation))
	if text := api.last(t, bobTG.ID); !strings.Contains(text, "对应不到工单") {
		// Bob's chat has no such message: it is about no ticket for him.
		t.Fatalf("someone else's reply: %s", text)
	}
	if err := store.SaveMessageRef(ctx, bobTG.ID, 4141, ticketID); err != nil {
		t.Fatal(err)
	}
	send(Chat{ID: bobTG.ID, Type: "private"}, bobTG, "我也来", botMessage(4141))
	if text := api.last(t, bobTG.ID); !strings.Contains(text, "不属于你") {
		t.Fatalf("someone else's ticket: %s", text)
	}
	private(bobTG, "随便说点什么")
	if text := api.last(t, bobTG.ID); !strings.Contains(text, "回复（引用）") {
		t.Fatalf("plain text in private: %s", text)
	}

	// Staff answer in the staff group by replying to the notice.
	if err := store.SaveMessageRef(ctx, staffChatID, 4242, ticketID); err != nil {
		t.Fatal(err)
	}
	staffChat := Chat{ID: staffChatID, Type: "supergroup"}
	send(staffChat, readerTG, "看看", botMessage(4242))
	if text := api.last(t, staffChatID); !strings.Contains(text, "没有这个权限") {
		t.Fatalf("read-only staff: %s", text)
	}
	send(staffChat, adminTG, "请检查防火墙", botMessage(4242))
	if text := api.last(t, staffChatID); !strings.Contains(text, "已回复工单") {
		t.Fatalf("staff reply: %s", text)
	}
	if status := count(`SELECT count(*) FROM support_tickets WHERE id=$1 AND status='staff_reply' AND assigned_staff_id=$2`, ticketID, admin); status != 1 {
		t.Fatal("the staff reply did not update the ticket")
	}
	events.wait(t, 1, 2)
	press(adminTG, staffChatID, callbackClaim+ticketID)
	if edit := api.lastEdit(); !strings.Contains(edit, "已由 Admin 认领") {
		t.Fatalf("claim: %q", edit)
	}
	press(readerTG, staffChatID, callbackClaim+ticketID)
	if answer := api.lastAnswer(); !strings.Contains(answer, "没有这个权限") {
		t.Fatalf("read-only claim: %q", answer)
	}
	if _, err := db.Exec(ctx, `UPDATE support_tickets SET status='closed' WHERE id=$1`, ticketID); err != nil {
		t.Fatal(err)
	}
	send(Chat{ID: aliceTG.ID, Type: "private"}, aliceTG, "还在吗", botMessage(confirmation))
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "已关闭") {
		t.Fatalf("closed ticket: %s", text)
	}

	// ---- Streak bonus ----
	for _, day := range []string{"2026-09-28", "2026-09-29"} {
		if _, err := db.Exec(ctx, `INSERT INTO telegram_checkins(account_id,day,telegram_id,amount_minor,created_at) VALUES($1,$2::date,$3,30,$2::date+interval '9 hours')`, alice.account, day, aliceTG.ID); err != nil {
			t.Fatal(err)
		}
	}
	before := balance(alice)
	group(aliceTG, "签到")
	if text := api.last(t, groupID); !strings.Contains(text, "连续签到 3 天") || !strings.Contains(text, "额外奖励") {
		t.Fatalf("streak bonus: %s", text)
	}
	if got := balance(alice) - before; got != 30+70 {
		t.Fatalf("check-in with a bonus paid %d", got)
	}
	group(bobTG, "签到")
	group(aliceTG, "/rank")
	if text := api.last(t, groupID); !strings.Contains(text, "本周签到榜") || strings.Index(text, "Alice") > strings.Index(text, "Bob") {
		t.Fatalf("/rank: %s", text)
	}

	// ---- Red packets ----
	group(eveTG, "/redpacket 1 3")
	if text := api.last(t, groupID); !strings.Contains(text, "管理员可以发红包") {
		t.Fatalf("red packet by a member: %s", text)
	}
	group(adminTG, "/redpacket 1 3")
	buttons := api.lastButtons(groupID)
	if len(buttons) != 1 || !strings.HasPrefix(buttons[0][0].Data, callbackRedPacket) {
		t.Fatalf("red packet post: %s %+v", api.last(t, groupID), buttons)
	}
	packet := strings.TrimPrefix(buttons[0][0].Data, callbackRedPacket)
	before = balance(alice) + balance(bob) + balance(carol)
	press(aliceTG, groupID, callbackRedPacket+packet)
	if answer := api.lastAnswer(); !strings.Contains(answer, "领到") {
		t.Fatalf("first share: %q", answer)
	}
	press(aliceTG, groupID, callbackRedPacket+packet)
	if answer := api.lastAnswer(); !strings.Contains(answer, "已经领过") {
		t.Fatalf("second share: %q", answer)
	}
	press(eveTG, groupID, callbackRedPacket+packet)
	if answer := api.lastAnswer(); !strings.Contains(answer, "绑定站点账号") {
		t.Fatalf("unlinked share: %q", answer)
	}
	press(bobTG, groupID, callbackRedPacket+packet)
	press(carolTG, groupID, callbackRedPacket+packet)
	if got := balance(alice) + balance(bob) + balance(carol) - before; got != 100 {
		t.Fatalf("the shares add up to %d, want 100", got)
	}
	if n := count(`SELECT count(*) FROM telegram_red_packets WHERE id=$1 AND status='finished' AND remaining_minor=0`, packet); n != 1 {
		t.Fatal("the red packet is not finished")
	}
	if edit := api.lastEdit(); !strings.Contains(edit, "已被领完") {
		t.Fatalf("finished post: %q", edit)
	}

	// A password packet for accounts that paid for something.
	if _, err := bot.CreateRedPacket(ctx, postgres.RedPacketInput{CreatedBy: admin, TotalMinor: 50, Count: 2, Password: "新年快乐", RequireSpent: true, ExpiresAt: now.Add(time.Hour)}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := bot.CreateRedPacket(ctx, postgres.RedPacketInput{CreatedBy: admin, TotalMinor: 50, Count: 2, Password: "新年快乐", ExpiresAt: now.Add(time.Hour)}, false); err != postgres.ErrRedPacketPassword {
		t.Fatalf("a second packet with the password: %v", err)
	}
	group(aliceTG, "新年快乐")
	if text := api.last(t, groupID); !strings.Contains(text, "消费过的账号") {
		t.Fatalf("password packet without spending: %s", text)
	}
	if _, err := db.Exec(ctx, `INSERT INTO invoices(number,account_id,status,currency,subtotal_minor,total_minor,balance_minor,due_at,paid_at,kind) VALUES('INV-PAID',$1,'paid','CNY',1000,1000,0,now(),now(),'initial')`, bob.account); err != nil {
		t.Fatal(err)
	}
	before = balance(bob)
	group(bobTG, "新年快乐")
	if text := api.last(t, groupID); !strings.Contains(text, "Bob") || !strings.Contains(text, "领到红包") || balance(bob) <= before {
		t.Fatalf("password packet: %s", text)
	}
	// A packet for accounts linked 30 days; then it runs out of time.
	group(adminTG, "/hb 0.5 2")
	late := strings.TrimPrefix(api.lastButtons(groupID)[0][0].Data, callbackRedPacket)
	if _, err := db.Exec(ctx, `UPDATE telegram_red_packets SET min_linked_days=30 WHERE id=$1`, late); err != nil {
		t.Fatal(err)
	}
	press(carolTG, groupID, callbackRedPacket+late)
	if answer := api.lastAnswer(); !strings.Contains(answer, "绑定满 30 天") {
		t.Fatalf("too new for a packet: %q", answer)
	}
	now = now.Add(RedPacketTTL + time.Minute)
	bot.settle(ctx, api, current())
	if n := count(`SELECT count(*) FROM telegram_red_packets WHERE status='expired'`); n != 2 {
		t.Fatalf("expired packets = %d", n)
	}

	// ---- Leaderboards: last week's, on Monday at 10:00, once ----
	now = time.Date(2026, 10, 5, 10, 30, 0, 0, clock.Zone)
	before = balance(alice)
	bot.settle(ctx, api, current())
	bot.settle(ctx, api, current())
	boards := 0
	api.mu.Lock()
	for _, sent := range api.sent {
		if sent.chat == groupID && strings.Contains(sent.text, "<b>上周签到榜") {
			boards++
			if !strings.Contains(sent.text, "1. Alice — 3") || !strings.Contains(sent.text, "2. Bob — 1") {
				t.Errorf("board: %s", sent.text)
			}
		}
	}
	api.mu.Unlock()
	if boards != 1 || balance(alice)-before != 500 {
		t.Fatalf("boards posted %d, prize %d", boards, balance(alice)-before)
	}
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "第 1 名") {
		t.Fatalf("winner told: %s", text)
	}

	// ---- New members: held until they press the button, removed after the time ----
	join := func(user User) {
		bot.handle(ctx, api, current(), Update{ChatMember: &ChatMemberUpdated{Chat: Chat{ID: groupID, Type: "supergroup"}, OldChatMember: ChatMember{Status: "left", User: user}, NewChatMember: ChatMember{Status: "member", User: user}}})
	}
	join(eveTG)
	if allowed, held := api.restricted[eveTG.ID]; !held || allowed {
		t.Fatal("a new member was not held")
	}
	if buttons := api.lastButtons(groupID); len(buttons) != 1 || buttons[0][0].Data != "vf:601" {
		t.Fatalf("check buttons: %+v", buttons)
	}
	press(bobTG, groupID, "vf:601")
	if answer := api.lastAnswer(); !strings.Contains(answer, "不是给你") {
		t.Fatalf("someone else's check: %q", answer)
	}
	press(eveTG, groupID, "vf:601")
	if !api.restricted[eveTG.ID] {
		t.Fatal("the member was not let in")
	}
	join(daveTG)
	join(carolTG)
	if _, held := api.restricted[carolTG.ID]; held {
		t.Fatal("a member with a linked account was held")
	}
	now = now.Add(11 * time.Minute)
	bot.settle(ctx, api, current())
	if len(api.kicked) != 1 || api.kicked[0] != daveTG.ID {
		t.Fatalf("removed: %v", api.kicked)
	}

	// ---- The group's filters ----
	deleted := func(id int64) bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		for _, gone := range api.deleted {
			if gone == id {
				return true
			}
		}
		return false
	}
	if id := group(eveTG, "看看 https://spam.example"); !deleted(id) {
		t.Fatal("a link from an unlinked member stayed")
	}
	if id := group(aliceTG, "官网 https://cloud.example.com"); deleted(id) {
		t.Fatal("a linked member's link was removed")
	}
	if id := group(ownerTG, "https://t.me/announcements"); deleted(id) {
		t.Fatal("a group administrator's link was removed")
	}
	if id := group(aliceTG, "一起来赌博吧"); !deleted(id) {
		t.Fatal("a blocked word stayed")
	}
	var last int64
	for i := 0; i < 4; i++ {
		last = group(eveTG, "你好")
	}
	if !deleted(last) || deleted(last-1) {
		t.Fatal("the rate limit for unlinked members did not hold")
	}
}
