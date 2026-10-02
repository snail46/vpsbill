package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
	"vpsbill/internal/telegram"
)

// New plans and restocks reach the announcement chat within the daily
// limit, customers waiting for a plan are told once, and the staff group
// is one of the merchant's recipients.
func TestStockAndStaffGroupIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "stock")
	box, err := security.NewSecretBox(strings.Repeat("cd", 32))
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
	const announceChat, staffChat = int64(-1009000000001), int64(-1009000000002)
	bot := settings.DefaultTelegramSettings()
	bot.Enabled, bot.BotToken, bot.BotUsername, bot.ChatID = true, "1:token", "site_bot", -100
	bot.AnnounceChatID, bot.AnnounceDailyCap, bot.AdminChatID = announceChat, 2, staffChat
	if err = runtime.SetTelegram(ctx, bot, installed.Identity.UserID); err != nil {
		t.Fatal(err)
	}
	catalog := postgres.NewCatalogStore(db)
	n := New(postgres.NewMailStore(db), runtime, box, slog.New(slog.NewTextHandler(io.Discard, nil)))

	type queued struct {
		channel, recipient, subject, body, buttons string
	}
	queue := func() []queued {
		t.Helper()
		rows, err := db.Query(ctx, `SELECT channel,recipient,subject,body,coalesce(buttons::text,'') FROM mail_queue ORDER BY created_at,id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []queued
		for rows.Next() {
			var item queued
			if err := rows.Scan(&item.channel, &item.recipient, &item.subject, &item.body, &item.buttons); err != nil {
				t.Fatal(err)
			}
			result = append(result, item)
		}
		return result
	}

	// Plans that exist at the first look are only recorded.
	old, err := catalog.CreatePlan(ctx, postgres.Plan{Code: "OLD", Name: "Old", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true,
		Prices: []postgres.Price{{BillingCycle: "monthly", AmountMinor: 900}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE plans SET created_at=now()-interval '3 days' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	n.WatchStock(ctx)
	if got := queue(); len(got) != 0 {
		t.Fatalf("an existing plan was announced: %+v", got)
	}

	// A new plan is announced once, with what it is and a button to buy it.
	limit := 3
	fresh, err := catalog.CreatePlan(ctx, postgres.Plan{Code: "NEW", Name: "NAT Mini", Virtualization: "lxc", VCPU: 1, RAMMB: 2048, DiskGB: 10, TrafficGB: 300, DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true,
		StockLimit: &limit, Prices: []postgres.Price{{BillingCycle: "quarterly", AmountMinor: 5000}, {BillingCycle: "monthly", AmountMinor: 1980}}})
	if err != nil {
		t.Fatal(err)
	}
	n.WatchStock(ctx)
	n.WatchStock(ctx)
	got := queue()
	if len(got) != 1 || got[0].channel != "telegram" || got[0].recipient != "-1009000000001" || !strings.Contains(got[0].subject, "新品上架：NAT Mini") ||
		!strings.Contains(got[0].body, "1 核 · 2 GB 内存 · 10 GB 硬盘 · 300 GB 月流量") || !strings.Contains(got[0].body, "¥19.80 / 月") || !strings.Contains(got[0].body, "库存：3 台") {
		t.Fatalf("new plan announcement: %+v", got)
	}
	var buttons [][]telegram.Button
	if err = json.Unmarshal([]byte(got[0].buttons), &buttons); err != nil || len(buttons) != 1 || buttons[0][0].URL != "https://cloud.example.com/portal/shop" {
		t.Fatalf("announcement buttons: %s err=%v", got[0].buttons, err)
	}

	// Sold out, a customer asks to be told; the restock tells them once
	// through their channels and is announced.
	auth := postgres.NewAuthStore(db)
	waiting, err := auth.RegisterCustomer(ctx, "waiting@example.com", "Waiting", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO telegram_links(user_id,account_id,telegram_id) VALUES($1,$2,7001)`, waiting.UserID, waiting.AccountID); err != nil {
		t.Fatal(err)
	}
	if err = postgres.NewTelegramStore(db).SetNotifyChannels(ctx, waiting.UserID, postgres.NotifyChannels{Telegram: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE plans SET stock_limit=0 WHERE id=$1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	n.WatchStock(ctx)
	if err = catalog.WatchPlan(ctx, fresh.ID, waiting.UserID); err != nil {
		t.Fatal(err)
	}
	if err = catalog.WatchPlan(ctx, fresh.ID, waiting.UserID); err != nil {
		t.Fatalf("watching twice: %v", err)
	}
	if err = catalog.WatchPlan(ctx, "00000000-0000-0000-0000-000000000000", waiting.UserID); err != postgres.ErrPlanNotFound {
		t.Fatalf("watching a plan that does not exist: %v", err)
	}
	if ids, err := catalog.WatchedPlans(ctx, waiting.UserID); err != nil || len(ids) != 1 || ids[0] != fresh.ID {
		t.Fatalf("watched plans: %v err=%v", ids, err)
	}
	if len(queue()) != 1 {
		t.Fatalf("selling out queued something: %+v", queue())
	}
	if _, err = db.Exec(ctx, `UPDATE plans SET stock_limit=5 WHERE id=$1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	n.WatchStock(ctx)
	n.WatchStock(ctx)
	got = queue()
	if len(got) != 3 {
		t.Fatalf("restock queued %d messages: %+v", len(got), got)
	}
	var toWatcher, toChat *queued
	for index := range got[1:] {
		item := &got[1+index]
		if item.recipient == "7001" {
			toWatcher = item
		} else if item.recipient == "-1009000000001" {
			toChat = item
		}
	}
	if toWatcher == nil || !strings.Contains(toWatcher.subject, "NAT Mini 到货了") || !strings.Contains(toWatcher.body, "库存：5 台") || !strings.Contains(toWatcher.buttons, "/portal/shop") {
		t.Fatalf("notice to the waiting customer: %+v", toWatcher)
	}
	if toChat == nil || !strings.Contains(toChat.subject, "补货：NAT Mini") {
		t.Fatalf("restock announcement: %+v", toChat)
	}
	if ids, _ := catalog.WatchedPlans(ctx, waiting.UserID); len(ids) != 0 {
		t.Fatalf("the watch was kept after its notice: %v", ids)
	}

	// The day's limit of two announcements is reached: the next restock is
	// not announced.
	if _, err = db.Exec(ctx, `UPDATE plans SET stock_limit=0 WHERE id=$1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	n.WatchStock(ctx)
	if _, err = db.Exec(ctx, `UPDATE plans SET stock_limit=5 WHERE id=$1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	n.WatchStock(ctx)
	if got = queue(); len(got) != 3 {
		t.Fatalf("announced past the daily limit: %+v", got)
	}

	// The staff group hears what merchant mail says.
	recipients := n.adminRecipients(ctx)
	if len(recipients) != 2 || recipients[0] != "admin@example.com" || recipients[1] != adminChat {
		t.Fatalf("merchant recipients: %v", recipients)
	}
	n.enqueue(ctx, adminChat, "[Test Cloud] 新工单", "正文", "ticket:1:"+adminChat)
	n.enqueue(ctx, adminChat, "[Test Cloud] 新工单", "正文", "ticket:1:"+adminChat)
	got = queue()
	if len(got) != 4 || got[3].channel != "telegram" || got[3].recipient != "-1009000000002" {
		t.Fatalf("staff group notice: %+v", got)
	}
}
