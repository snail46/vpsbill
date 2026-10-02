package telegram

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
)

// The reward period, the account commands, paying an invoice from the
// balance with a confirmation, and the rebate for an invited member's
// first payment.
func TestBotAccountIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "telegramaccount")
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
	now := time.Now()
	cfg := settings.DefaultTelegramSettings()
	cfg.Enabled, cfg.BotToken, cfg.BotUsername, cfg.ChatID = true, "123:token", "site_bot", groupID
	cfg.CheckinMinMinor, cfg.CheckinMaxMinor, cfg.ReplyTTLSeconds, cfg.DailyBudgetMinor = 30, 30, 0, 0
	cfg.RebatePercent, cfg.RebateMaxMinor, cfg.RebateDelayHours = 10, 300, 0
	save := func() {
		t.Helper()
		if err := runtime.SetTelegram(ctx, cfg, admin); err != nil {
			t.Fatal(err)
		}
	}
	future, past := now.Add(48*time.Hour), now.Add(-48*time.Hour)
	cfg.RewardsFrom = &future
	save()

	store := postgres.NewTelegramStore(db)
	billing := postgres.NewBillingStore(db)
	api := &fakeAPI{}
	bot := New(store, runtime, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bot.connect = func(string, string) API { return api }
	bot.now = func() time.Time { return now }
	current := func() settings.TelegramSettings { return runtime.Current().TelegramLedger() }

	alice := newCustomer(t, ctx, db, "alice@example.com", "zh", true)
	bob := newCustomer(t, ctx, db, "bob@example.com", "en", true)
	aliceTG, bobTG := User{ID: 501, FirstName: "Alice"}, User{ID: 502, FirstName: "Bob"}
	for _, link := range []struct {
		c    customer
		user User
	}{{alice, aliceTG}, {bob, bobTG}} {
		if _, err := db.Exec(ctx, `INSERT INTO telegram_links(user_id,account_id,telegram_id,first_name) VALUES($1,$2,$3,$4)`, link.c.user, link.c.account, link.user.ID, link.user.FirstName); err != nil {
			t.Fatal(err)
		}
	}
	private := func(user User, text string) {
		bot.handle(ctx, api, current(), Update{Message: &Message{MessageID: 9, From: &user, Chat: Chat{ID: user.ID, Type: "private"}, Text: text}})
	}
	group := func(user User, text string) {
		bot.handle(ctx, api, current(), Update{Message: &Message{MessageID: 9, From: &user, Chat: Chat{ID: groupID, Type: "supergroup"}, Text: text}})
	}
	press := func(user User, data string) {
		bot.handle(ctx, api, current(), Update{Callback: &CallbackQuery{ID: "q", From: user, Data: data, Message: &Message{MessageID: 77, Chat: Chat{ID: user.ID, Type: "private"}}}})
	}
	count := func(query string, args ...any) (value int64) {
		t.Helper()
		if err := db.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	balance := func(c customer) int64 { return count(`SELECT balance_minor FROM accounts WHERE id=$1`, c.account) }

	// Before the period nothing is paid, and the bot says when it starts.
	group(aliceTG, "签到")
	if text := api.last(t, groupID); !strings.Contains(text, "还没有开始") || !strings.Contains(text, "活动自") || count(`SELECT count(*) FROM telegram_checkins`) != 0 {
		t.Fatalf("check-in before the period: %s", text)
	}
	private(aliceTG, "/help")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "还没有开始") {
		t.Fatalf("help before the period: %s", text)
	}
	cfg.RewardsFrom, cfg.RewardsUntil = &past, &future
	save()
	group(aliceTG, "签到")
	if text := api.last(t, groupID); !strings.Contains(text, "签到成功") || balance(alice) != 30 {
		t.Fatalf("check-in inside the period: %s balance=%d", text, balance(alice))
	}
	private(aliceTG, "/help")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "活动时间") || !strings.Contains(text, "10%") {
		t.Fatalf("help inside the period: %s", text)
	}

	// The account commands answer in private only.
	group(aliceTG, "/balance")
	if text := api.last(t, groupID); !strings.Contains(text, "私聊") {
		t.Fatalf("/balance in the group: %s", text)
	}
	private(aliceTG, "/balance")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "¥0.30") || !strings.Contains(text, "活动奖励") {
		t.Fatalf("/balance: %s", text)
	}
	private(aliceTG, "/services")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "还没有 VPS") {
		t.Fatalf("/services: %s", text)
	}
	private(User{ID: 999, FirstName: "Nobody"}, "/invoices")
	if text := api.last(t, 999); !strings.Contains(text, "还没有绑定") {
		t.Fatalf("/invoices without a link: %s", text)
	}
	private(aliceTG, "/invoices")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "没有待支付") {
		t.Fatalf("/invoices with nothing to pay: %s", text)
	}

	// An order to pay: listed with a button, refused without the money,
	// confirmed before it is charged, and charged once.
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	plan, err := postgres.NewCatalogStore(db).CreatePlan(ctx, postgres.Plan{Code: "SMALL", Name: "Small", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, TrafficGB: 100,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, Prices: []postgres.Price{{BillingCycle: "monthly", AmountMinor: 1900}}})
	if err != nil {
		t.Fatal(err)
	}
	order, err := billing.CreateOrder(ctx, postgres.CreateOrderInput{AccountID: alice.account, ActorType: "customer", Items: []postgres.OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
	if err != nil {
		t.Fatal(err)
	}
	private(aliceTG, "/invoices")
	if text, buttons := api.last(t, aliceTG.ID), api.lastButtons(aliceTG.ID); !strings.Contains(text, order.InvoiceNumber) || len(buttons) != 1 || buttons[0][0].Data != "inv:"+order.InvoiceID {
		t.Fatalf("/invoices: %s buttons=%+v", text, buttons)
	}
	press(aliceTG, "inv:"+order.InvoiceID)
	if text, buttons := api.last(t, aliceTG.ID), api.lastButtons(aliceTG.ID); !strings.Contains(text, "余额不足") || len(buttons) != 1 || !strings.Contains(buttons[0][0].URL, "/portal/wallet?need=1870#topup") {
		t.Fatalf("short of balance: %s buttons=%+v", text, buttons)
	}
	if _, err = billing.AdjustWallet(ctx, alice.account, admin, 5000, "seed"); err != nil {
		t.Fatal(err)
	}
	press(bobTG, "inv:"+order.InvoiceID)
	if answer := api.lastAnswer(); !strings.Contains(answer, "no longer valid") {
		t.Fatalf("someone else's invoice: %q", answer)
	}
	press(aliceTG, "inv:"+order.InvoiceID)
	text, buttons := api.last(t, aliceTG.ID), api.lastButtons(aliceTG.ID)
	if !strings.Contains(text, "确认用余额支付") || !strings.Contains(text, "¥19.00") || !strings.Contains(text, "¥31.30") || len(buttons) != 1 || len(buttons[0]) != 2 || buttons[0][0].Data != "pay:"+order.InvoiceID || buttons[0][1].Data != "x" {
		t.Fatalf("confirmation: %s buttons=%+v", text, buttons)
	}
	if balance(alice) != 5030 {
		t.Fatalf("charged before the confirmation: %d", balance(alice))
	}
	press(aliceTG, "x")
	if text := api.lastEdit(); !strings.Contains(text, "已取消") || balance(alice) != 5030 {
		t.Fatalf("cancel: %s balance=%d", text, balance(alice))
	}
	press(aliceTG, "pay:"+order.InvoiceID)
	if text := api.lastEdit(); !strings.Contains(text, "已用余额支付") || balance(alice) != 3130 || count(`SELECT count(*) FROM invoices WHERE id=$1 AND status='paid'`, order.InvoiceID) != 1 {
		t.Fatalf("pay: %s balance=%d", text, balance(alice))
	}
	press(aliceTG, "pay:"+order.InvoiceID)
	if answer := api.lastAnswer(); !strings.Contains(answer, "已经支付") || balance(alice) != 3130 {
		t.Fatalf("paid twice: %q balance=%d", answer, balance(alice))
	}
	private(aliceTG, "/services")
	if text := api.last(t, aliceTG.ID); !strings.Contains(text, "1 台") || !strings.Contains(text, "Small") {
		t.Fatalf("/services after buying: %s", text)
	}

	// Bob was invited by Alice. His payment from the balance earns her
	// nothing; his first real payment earns her 10%, capped at 3.00, once.
	if _, err = db.Exec(ctx, `INSERT INTO telegram_invites(invitee_telegram_id,inviter_account_id,invitee_name,status,joined_at) VALUES($1,$2,'Bob','rewarded',now()-interval '1 day')`, bobTG.ID, alice.account); err != nil {
		t.Fatal(err)
	}
	pay := func(amount int64, provider, reference string) {
		t.Helper()
		topup, err := billing.CreateTopupInvoice(ctx, bob.account, bob.user, amount)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := billing.ProcessPayment(ctx, postgres.PaymentEvent{Provider: provider, ProviderEventID: reference, EventType: "payment.succeeded", ProviderTransactionID: reference, InvoiceNumber: topup.Number, AmountMinor: amount, Currency: "CNY", Payload: []byte(`{}`)}, "payment_provider", provider); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `INSERT INTO transactions(account_id,provider,provider_transaction_id,type,status,currency,amount_minor) VALUES($1,'balance','balance:x','payment','succeeded','CNY',900)`, bob.account); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	bot.settle(ctx, api, current())
	if balance(alice) != 3130 || count(`SELECT count(*) FROM telegram_rebates`) != 0 {
		t.Fatalf("a balance payment earned a rebate: balance=%d", balance(alice))
	}
	pay(5000, "epay", "first")
	now = now.Add(2 * time.Minute)
	bot.settle(ctx, api, current())
	if text := api.last(t, aliceTG.ID); balance(alice) != 3430 || !strings.Contains(text, "返利") || !strings.Contains(text, "¥3.00") {
		t.Fatalf("rebate: balance=%d message=%s", balance(alice), text)
	}
	pay(8000, "epay", "second")
	now = now.Add(2 * time.Minute)
	bot.settle(ctx, api, current())
	if balance(alice) != 3430 || count(`SELECT count(*) FROM telegram_rebates`) != 1 {
		t.Fatalf("a second payment earned another rebate: balance=%d", balance(alice))
	}

	// After the period nothing more is paid.
	cfg.RewardsUntil = &past
	cfg.RewardsFrom = nil
	save()
	now = now.Add(26 * time.Hour)
	group(aliceTG, "签到")
	if text := api.last(t, groupID); !strings.Contains(text, "已经结束") || balance(alice) != 3430 {
		t.Fatalf("check-in after the period: %s balance=%d", text, balance(alice))
	}
}
