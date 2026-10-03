package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/config"
	"vpsbill/internal/mail"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
	"vpsbill/internal/telegram"
)

// A notice goes to the channels its reader chose: mail, the linked
// Telegram account, or both, and to the mailbox while Telegram cannot be
// used.
func TestNotificationChannelsIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "channels")
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
	view := runtime.SiteView()
	if _, err = runtime.UpdateSite(ctx, settings.SiteInput{
		AppName: view.AppName, PublicURL: view.PublicURL, SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPFrom: "noreply@example.com", SMTPSecurity: "starttls",
		MailNotifications: view.MailNotifications, TicketAttachmentMaxMB: view.TicketAttachmentMaxMB, Marketplace: view.Marketplace,
	}, installed.Identity.UserID); err != nil {
		t.Fatal(err)
	}
	bot := settings.DefaultTelegramSettings()
	bot.Enabled, bot.BotToken, bot.BotUsername, bot.ChatID = true, "1:token", "site_bot", -100
	if err = runtime.SetTelegram(ctx, bot, installed.Identity.UserID); err != nil {
		t.Fatal(err)
	}

	auth := postgres.NewAuthStore(db)
	links := postgres.NewTelegramStore(db)
	// name → Telegram id (0 = not linked) and the channels chosen.
	readers := []struct {
		name            string
		telegramID      int64
		email, telegram bool
	}{
		{"mail", 0, true, false},
		{"tg", 1001, false, true},
		{"both", 1002, true, true},
		{"blocked", 1003, false, true},
		{"linkedmail", 1004, true, false},
	}
	for _, reader := range readers {
		identity, err := auth.RegisterCustomer(ctx, reader.name+"@example.com", reader.name, "hash", true)
		if err != nil {
			t.Fatal(err)
		}
		if reader.telegramID != 0 {
			if _, err := db.Exec(ctx, `INSERT INTO telegram_links(user_id,account_id,telegram_id,username) VALUES($1,$2,$3,$4)`, identity.UserID, identity.AccountID, reader.telegramID, reader.name); err != nil {
				t.Fatal(err)
			}
		}
		channels := postgres.NotifyChannels{Email: reader.email, Telegram: reader.telegram}
		if err := links.SetNotifyChannels(ctx, identity.UserID, channels); err != nil {
			t.Fatalf("%s chooses %+v: %v", reader.name, channels, err)
		}
		if got, err := links.NotifyChannels(ctx, identity.UserID); err != nil || got != channels {
			t.Fatalf("%s channels = %+v err=%v", reader.name, got, err)
		}
		// Neither channel is no choice, and Telegram needs a linked account.
		if err := links.SetNotifyChannels(ctx, identity.UserID, postgres.NotifyChannels{}); !errors.Is(err, postgres.ErrNotifyChannels) {
			t.Fatalf("%s chose no channel: %v", reader.name, err)
		}
		if reader.telegramID == 0 {
			if err := links.SetNotifyChannels(ctx, identity.UserID, postgres.NotifyChannels{Telegram: true}); !errors.Is(err, postgres.ErrTelegramNotLinked) {
				t.Fatalf("%s chose Telegram without a link: %v", reader.name, err)
			}
		}
	}

	n := New(postgres.NewMailStore(db), runtime, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, reader := range readers {
		n.TradeCompleted(ctx, postgres.TradeResult{ListingID: "l-" + reader.name, InstanceName: "vm<1>", PlanName: "NAT", Currency: "CNY", PriceMinor: 2000,
			SellerEmail: reader.name + "@example.com", SellerName: reader.name})
		// The same event twice is told once on each channel.
		n.TradeCompleted(ctx, postgres.TradeResult{ListingID: "l-" + reader.name, InstanceName: "vm<1>", PlanName: "NAT", Currency: "CNY", PriceMinor: 2000,
			SellerEmail: reader.name + "@example.com", SellerName: reader.name})
	}
	queued := func() map[string]string {
		rows, err := db.Query(ctx, `SELECT channel,recipient,status FROM mail_queue ORDER BY channel,recipient`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string]string{}
		for rows.Next() {
			var channel, recipient, status string
			if err := rows.Scan(&channel, &recipient, &status); err != nil {
				t.Fatal(err)
			}
			if _, twice := result[channel+":"+recipient]; twice {
				t.Errorf("%s:%s was queued twice", channel, recipient)
			}
			result[channel+":"+recipient] = status
		}
		return result
	}
	want := []string{"mail:mail@example.com", "mail:both@example.com", "mail:linkedmail@example.com", "telegram:1001", "telegram:1002", "telegram:1003"}
	got := queued()
	if len(got) != len(want) {
		t.Fatalf("queued %v, want %v", got, want)
	}
	for _, key := range want {
		if got[key] != "pending" {
			t.Fatalf("queued %v, want %v", got, want)
		}
	}

	mailed, sent := []string{}, map[int64]string{}
	n.send = func(_ context.Context, _ mail.Config, to, _, _ string) error {
		mailed = append(mailed, to)
		return nil
	}
	n.sendTelegram = func(_ context.Context, config settings.TelegramSettings, chatID int64, text string, _ [][]telegram.Button) (int64, error) {
		if config.BotToken != "1:token" {
			t.Errorf("sent with token %q", config.BotToken)
		}
		if chatID == 1003 {
			return 0, &telegram.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}
		}
		sent[chatID] = text
		return int64(len(sent)), nil
	}
	n.sendDue(ctx)
	if len(mailed) != 3 || len(sent) != 2 {
		t.Fatalf("mailed %v, sent on Telegram %v", mailed, sent)
	}
	if text := sent[1001]; !strings.HasPrefix(text, "<b>[Test Cloud] ") || !strings.Contains(text, "vm&lt;1&gt;") || strings.Contains(text, "vm<1>") {
		t.Fatalf("Telegram text is not escaped HTML with the subject in bold:\n%s", text)
	}
	got = queued()
	for key, status := range map[string]string{"mail:mail@example.com": "sent", "telegram:1001": "sent", "telegram:1002": "sent", "telegram:1003": "failed"} {
		if got[key] != status {
			t.Errorf("%s is %s, want %s", key, got[key], status)
		}
	}

	// A ticket's notices on Telegram say how to answer them and remember
	// their ticket, so a reply lands there; the staff group's carries a
	// button to claim the ticket.
	bot.AdminChatID = -200
	if err = runtime.SetTelegram(ctx, bot, installed.Identity.UserID); err != nil {
		t.Fatal(err)
	}
	var tgAccount, tgUserID string
	if err = db.QueryRow(ctx, `SELECT m.account_id,u.id FROM users u JOIN memberships m ON m.user_id=u.id WHERE u.email='tg@example.com'`).Scan(&tgAccount, &tgUserID); err != nil {
		t.Fatal(err)
	}
	ticket, err := postgres.NewOperationsStore(db).CreateTicket(ctx, tgAccount, tgUserID, "", "SSH is down", "normal", "help", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	n.TicketCreated(ctx, ticket.Ticket.ID, ticket.Messages[0].ID, "help")
	n.TicketReplied(ctx, ticket.Ticket.ID, "m-staff", "staff", "try again", false)
	sent = map[int64]string{}
	buttons := map[int64][][]telegram.Button{}
	n.sendTelegram = func(_ context.Context, _ settings.TelegramSettings, chatID int64, text string, rows [][]telegram.Button) (int64, error) {
		sent[chatID], buttons[chatID] = text, rows
		return 4000 + int64(len(sent)), nil
	}
	n.sendDue(ctx)
	if text := sent[-200]; !strings.Contains(text, "SSH is down") || !strings.Contains(text, "回复这条消息") {
		t.Fatalf("staff group ticket notice: %s", text)
	}
	if rows := buttons[-200]; len(rows) != 1 || rows[0][0].Data != "tk:"+ticket.Ticket.ID {
		t.Fatalf("staff group claim button: %+v", rows)
	}
	if text := sent[1001]; !strings.Contains(text, "回复（引用）这条消息") {
		t.Fatalf("customer ticket notice: %s", text)
	}
	var refs int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM telegram_message_refs WHERE ticket_id=$1 AND chat_id IN (-200,1001)`, ticket.Ticket.ID).Scan(&refs); err != nil || refs != 2 {
		t.Fatalf("remembered %d ticket messages, err=%v", refs, err)
	}
	if _, err = db.Exec(ctx, `DELETE FROM mail_queue WHERE dedup_key LIKE 'ticket-message:%'`); err != nil {
		t.Fatal(err)
	}

	// Without the bot, a Telegram-only reader is told by mail; unlinking
	// sends notices to the mailbox again.
	bot.Enabled = false
	if err = runtime.SetTelegram(ctx, bot, installed.Identity.UserID); err != nil {
		t.Fatal(err)
	}
	n.TradeCompleted(ctx, postgres.TradeResult{ListingID: "l-fallback", InstanceName: "vm-2", PlanName: "NAT", Currency: "CNY", PriceMinor: 2000, SellerEmail: "tg@example.com", SellerName: "tg"})
	if got = queued(); got["mail:tg@example.com"] != "pending" {
		t.Fatalf("no mail fallback while the bot is off: %v", got)
	}
	var tgUser string
	if err = db.QueryRow(ctx, `SELECT id FROM users WHERE email='both@example.com'`).Scan(&tgUser); err != nil {
		t.Fatal(err)
	}
	if err = links.Unlink(ctx, tgUser, time.Now().Add(postgres.TelegramUnlinkCooldown+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if channels, err := links.NotifyChannels(ctx, tgUser); err != nil || !channels.Email || channels.Telegram {
		t.Fatalf("channels after unlinking: %+v err=%v", channels, err)
	}
}
