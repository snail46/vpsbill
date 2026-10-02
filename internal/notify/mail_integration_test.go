package notify

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// Mail is written in the language the recipient reads the site in, and
// both languages of every message take the same arguments.
func TestMailLanguageIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	if !strings.Contains(databaseURL, "vpsbill_test") {
		t.Fatal("refusing to reset database without vpsbill_test in TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
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
	for _, user := range [][2]string{{"zh@example.com", "zh"}, {"en@example.com", "en"}, {"none@example.com", ""}} {
		if _, err := db.Exec(ctx, `INSERT INTO users(email,display_name,password_hash,status,locale,email_verified_at) VALUES($1,$1,'x','active',$2,now())`, user[0], user[1]); err != nil {
			t.Fatal(err)
		}
	}
	n := New(postgres.NewMailStore(db), runtime, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := n.lang(ctx, "EN@example.com"); got != "en" {
		t.Fatalf("lang of an English reader = %s", got)
	}
	if got := n.lang(ctx, "none@example.com") + n.lang(ctx, "stranger@example.com"); got != "zhzh" {
		t.Fatalf("lang without a choice = %s", got)
	}

	now := time.Now()
	for _, to := range []string{"zh@example.com", "en@example.com"} {
		n.HostNodeOffline(ctx, postgres.OfflineHostedNode{ID: "n-" + to, Name: "hk-01", OwnerEmail: to, OwnerName: "Owner", LastSeenAt: now}, 24*time.Hour)
		n.NodeCleared(ctx, postgres.ClearanceResult{
			NodeID: "n-" + to, NodeName: "hk-01", Reason: "母机离线超过 24 小时，系统自动清退", Currency: "CNY", HostEmail: to, HostName: "Owner", PenaltyMinor: 500,
			Services: []postgres.ClearedService{{ServiceID: "s-" + to, InstanceName: "vm-1", BuyerName: "Buyer", BuyerEmail: to, RemainingMinor: 500, PenaltyMinor: 500, RefundMinor: 1000}},
		})
		n.ServiceRefunded(ctx, postgres.RefundResult{
			RefundQuote: postgres.RefundQuote{ServiceID: "s-" + to, InstanceName: "vm-1", Currency: "CNY", RefundMinor: 640},
			PlanName:    "NAT", HostMinor: 350, BuyerEmail: to, BuyerName: "Buyer", HostEmail: to, HostName: "Owner"})
		n.TradeCompleted(ctx, postgres.TradeResult{ListingID: "l-" + to, InstanceName: "vm-1", PlanName: "NAT", Currency: "CNY", PriceMinor: 2000,
			SellerEmail: to, SellerName: "Seller", BuyerEmail: to, BuyerName: "Buyer"})
	}
	rows, err := db.Query(ctx, `SELECT recipient,subject,body FROM mail_queue ORDER BY recipient,subject`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	han := func(text string) bool {
		for _, r := range text {
			if r >= 0x4e00 && r <= 0x9fff {
				return true
			}
		}
		return false
	}
	for rows.Next() {
		var to, subject, body string
		if err := rows.Scan(&to, &subject, &body); err != nil {
			t.Fatal(err)
		}
		counts[to]++
		if strings.Contains(subject+body, "%!") {
			t.Errorf("a format lacks or has extra arguments in the mail to %s:\n%s\n%s", to, subject, body)
		}
		switch to {
		case "en@example.com":
			if han(subject + body) {
				t.Errorf("English mail has Chinese text:\n%s\n%s", subject, body)
			}
		case "zh@example.com":
			if !han(subject) || !han(body) {
				t.Errorf("Chinese mail is not in Chinese:\n%s\n%s", subject, body)
			}
		}
	}
	if counts["zh@example.com"] != 7 || counts["en@example.com"] != 7 {
		t.Fatalf("mail queued: %v, want 7 for each reader", counts)
	}
}
