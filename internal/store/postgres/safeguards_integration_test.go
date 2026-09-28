package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSafeguardsIntegration covers email verification, TOTP replay, the
// traffic lock, chat moderation, reports, capacity caps, renewal price
// locks and refunds of failed platform provisioning.
func TestSafeguardsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	if !strings.Contains(databaseURL, "vpsbill_test") {
		t.Fatal("refusing to reset database without vpsbill_test in TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthStore(db)
	billing := NewBillingStore(db)
	catalog := NewCatalogStore(db)
	market := NewMarketplaceStore(db)
	mail := NewMailStore(db)
	portal := NewPortalStore(db)

	// Sign-up: unverified until the mailed token is used; mail to an
	// unverified address is not queued.
	pending, err := auth.RegisterCustomer(ctx, "Pending@Example.com", "Pending", "hash", false)
	if err != nil || pending.EmailVerified {
		t.Fatalf("register: %+v err=%v", pending, err)
	}
	if queued, err := mail.EnqueueMail(ctx, "pending@example.com", "s", "b", "k1"); err != nil || queued {
		t.Fatalf("mail to unverified address queued=%v err=%v", queued, err)
	}
	if err := auth.CreateEmailVerification(ctx, pending.UserID, []byte("verify-token"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConfirmEmail(ctx, []byte("wrong")); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("bad token: %v", err)
	}
	if user, err := auth.ConfirmEmail(ctx, []byte("verify-token")); err != nil || user != pending.UserID {
		t.Fatalf("confirm: %s %v", user, err)
	}
	if _, err := auth.ConfirmEmail(ctx, []byte("verify-token")); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("token reused: %v", err)
	}
	if identity, err := auth.CustomerByEmail(ctx, "pending@example.com"); err != nil || !identity.EmailVerified {
		t.Fatalf("verified flag: %+v err=%v", identity, err)
	}
	if queued, _ := mail.EnqueueMail(ctx, "pending@example.com", "s", "b", "k2"); !queued {
		t.Fatal("mail to a verified address was not queued")
	}
	buyer, err := auth.RegisterCustomer(ctx, "buyer@example.com", "Buyer", "hash", true)
	if err != nil || !buyer.EmailVerified {
		t.Fatalf("auto-verified register: %+v err=%v", buyer, err)
	}

	// TOTP steps are single use.
	if ok, _ := auth.ConsumeTOTPStep(ctx, buyer.UserID, 100); !ok {
		t.Fatal("first TOTP use refused")
	}
	for _, step := range []int64{100, 99} {
		if ok, _ := auth.ConsumeTOTPStep(ctx, buyer.UserID, step); ok {
			t.Fatalf("TOTP step %d replayed", step)
		}
	}

	// A platform node, plan and a paid service.
	var regionID string
	if err := db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes(region_id,name,provider_type,base_url,api_key_ciphertext,status,virtualization_types,capacity,capacity_vcpu,capacity_ram_mb,capacity_disk_gb)
		VALUES($1,'n1','hatch','agent://n1','x','online','{lxc}','{}',8,8192,100) RETURNING id`, regionID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.CreatePlan(ctx, Plan{Code: "P1", Name: "Small", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, TrafficGB: 1,
		AssignNAT: true, PortMappingCount: 2, DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true,
		Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	buy := func() string {
		order, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: buyer.AccountID, Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
		if err != nil {
			t.Fatal(err)
		}
		var number string
		if err := db.QueryRow(ctx, `SELECT number FROM invoices WHERE id=$1`, order.InvoiceID).Scan(&number); err != nil {
			t.Fatal(err)
		}
		paid, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: number, EventType: "payment.succeeded", ProviderTransactionID: number, InvoiceNumber: number, AmountMinor: 1000, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", "")
		if err != nil {
			t.Fatal(err)
		}
		return paid.ServiceIDs[0]
	}

	// Traffic over the allowance (both directions) stops the instance and
	// blocks starting it until the month changes.
	serviceID := buy()
	if _, err := db.Exec(ctx, `UPDATE services SET status='active',node_id=$2,runtime_status='running' WHERE id=$1`, serviceID, nodeID); err != nil {
		t.Fatal(err)
	}
	rx, tx := int64(700<<20), int64(400<<20)
	if err := mail.RecordServiceTraffic(ctx, serviceID, rx+tx, &rx, &tx); err != nil {
		t.Fatal(err)
	}
	month := time.Now().UTC().Format("2006-01")
	if locked, err := mail.LockForTraffic(ctx, serviceID, month); err != nil || !locked {
		t.Fatalf("lock: %v %v", locked, err)
	}
	if locked, _ := mail.LockForTraffic(ctx, serviceID, month); locked {
		t.Fatal("locked twice in a month")
	}
	if _, err := db.Exec(ctx, `UPDATE services SET desired_runtime_status=NULL,runtime_status='stopped' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := portal.QueueServiceAction(ctx, buyer.AccountID, buyer.UserID, serviceID, "start", "", ""); !errors.Is(err, ErrTrafficLocked) {
		t.Fatalf("started a traffic-locked instance: %v", err)
	}
	if err := mail.UnlockTraffic(ctx, serviceID, "2999-01"); err != nil {
		t.Fatal(err)
	}
	var lockedMonth *string
	var starts int
	if err := db.QueryRow(ctx, `SELECT traffic_locked_month,(SELECT count(*) FROM provisioning_jobs WHERE service_id=$1 AND action='start') FROM services WHERE id=$1`, serviceID).Scan(&lockedMonth, &starts); err != nil || lockedMonth != nil || starts != 1 {
		t.Fatalf("unlock: month=%v starts=%d err=%v", lockedMonth, starts, err)
	}

	// A platform service whose provisioning failed is refunded in full.
	failed := buy()
	if _, err := db.Exec(ctx, `UPDATE services SET status='error',node_id=NULL WHERE id=$1`, failed); err != nil {
		t.Fatal(err)
	}
	if _, err := market.QuoteRefund(ctx, buyer.AccountID, serviceID, time.Now(), nil); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("working platform service offered a refund: %v", err)
	}
	quote, err := market.QuoteRefund(ctx, buyer.AccountID, failed, time.Now(), nil)
	if err != nil || !quote.Available || !quote.Full || quote.RefundMinor != 1000 {
		t.Fatalf("failed provision quote: %+v err=%v", quote, err)
	}
	before, _ := billing.Wallet(ctx, buyer.AccountID, 1)
	if _, err := market.RefundService(ctx, buyer.AccountID, failed, buyer.UserID, time.Now(), nil, 1000); err != nil {
		t.Fatal(err)
	}
	after, _ := billing.Wallet(ctx, buyer.AccountID, 1)
	if after.BalanceMinor != before.BalanceMinor+1000 {
		t.Fatalf("refund balance %d -> %d", before.BalanceMinor, after.BalanceMinor)
	}

	// Hosted node: reports, capacity cap, chat limits and mutes, and the
	// renewal price lock.
	host, err := auth.RegisterCustomer(ctx, "host@example.com", "Host", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	hostedNode, err := market.CreateHostedNode(ctx, host.AccountID, host.UserID, HostedNodeInput{Name: "h1", RegionID: regionID, Location: "HK", LineDescription: "BGP", ExpiresAt: time.Now().AddDate(1, 0, 0).Format("2006-01-02")},
		HostedNodeRegistration{BaseURL: "agent://h1", APIKeyCiphertext: []byte("x"), VirtualizationTypes: []string{"lxc"}, CapacityVCPU: 16, CapacityRAMMB: 32768, CapacityDiskGB: 500})
	if err != nil {
		t.Fatal(err)
	}
	vcpu := 4
	if err := market.SetCapacityCap(ctx, hostedNode, buyer.UserID, CapacityCap{VCPU: &vcpu}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpdateNodeHealth(ctx, hostedNode, "online", map[string]any{}, 16, 32768, 500); err != nil {
		t.Fatal(err)
	}
	var capacity int
	if err := db.QueryRow(ctx, `SELECT capacity_vcpu FROM nodes WHERE id=$1`, hostedNode).Scan(&capacity); err != nil || capacity != 4 {
		t.Fatalf("capacity cap not applied: %d err=%v", capacity, err)
	}
	if _, err := market.CreateReport(ctx, buyer.AccountID, buyer.UserID, ReportInput{TargetType: "node", NodeID: hostedNode, Reason: "oversell", Detail: "只有 1 核"}); err != nil {
		t.Fatal(err)
	}
	if _, err := market.CreateReport(ctx, host.AccountID, host.UserID, ReportInput{TargetType: "node", NodeID: hostedNode, Reason: "oversell"}); err == nil {
		t.Fatal("host reported own node")
	}
	reports, err := market.Reports(ctx)
	if err != nil || len(reports) != 1 || reports[0].Status != "open" {
		t.Fatalf("reports: %+v err=%v", reports, err)
	}
	if err := market.ResolveReport(ctx, reports[0].ID, buyer.UserID, "resolved", "已核定为 4 核"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < chatBurst; i++ {
		if _, err := market.PostChatMessage(ctx, hostedNode, "host", host.UserID, host.AccountID, "Host", "hi"); err != nil {
			t.Fatal(err)
		}
	}
	var limited *ChatLimitError
	if _, err := market.PostChatMessage(ctx, hostedNode, "host", host.UserID, host.AccountID, "Host", "flood"); !errors.As(err, &limited) {
		t.Fatalf("flood not limited: %v", err)
	}
	if _, err := market.PostChatMessage(ctx, hostedNode, "staff", buyer.UserID, "", "平台", "staff is not limited"); err != nil {
		t.Fatal(err)
	}
	if err := market.MuteChat(ctx, hostedNode, buyer.AccountID, buyer.UserID, "刷屏", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := market.PostChatMessage(ctx, hostedNode, "buyer", buyer.UserID, buyer.AccountID, "Buyer", "hello"); !errors.As(err, &limited) {
		t.Fatalf("muted account posted: %v", err)
	}
	if err := market.UnmuteChat(ctx, hostedNode, buyer.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := market.PostChatMessage(ctx, hostedNode, "buyer", buyer.UserID, buyer.AccountID, "Buyer", "hello"); err != nil {
		t.Fatalf("unmuted account: %v", err)
	}

	hostedPlan, err := catalog.CreatePlan(ctx, Plan{Code: "H1", Name: "Hosted", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5,
		AssignNAT: true, PortMappingCount: 2, DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true,
		OwnerAccountID: host.AccountID, NodeID: hostedNode, Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 2000}}})
	if err != nil {
		t.Fatal(err)
	}
	order, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: buyer.AccountID, Items: []OrderItemInput{{PlanID: hostedPlan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := billing.AdjustWallet(ctx, buyer.AccountID, buyer.UserID, 10000, "test"); err != nil {
		t.Fatal(err)
	}
	paid, err := billing.PayInvoiceWithBalance(ctx, buyer.AccountID, order.InvoiceID, buyer.UserID)
	if err != nil {
		t.Fatal(err)
	}
	hosted := paid.ServiceIDs[0]
	plan2 := hostedPlan
	plan2.Prices = []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 9000}}
	if _, err := catalog.UpdatePlan(ctx, hostedPlan.ID, plan2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE services SET status='active',next_due_at=now()+interval '1 day' WHERE id=$1`, hosted); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLifecycleStore(db).createRenewal(ctx, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var renewal int64
	if err := db.QueryRow(ctx, `SELECT total_minor FROM invoices WHERE service_id=$1 AND kind='renewal'`, hosted).Scan(&renewal); err != nil || renewal != 2000 {
		t.Fatalf("hosted renewal after a price rise = %d, want 2000 (err=%v)", renewal, err)
	}
}
