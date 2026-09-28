package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestAutoRenewIntegration covers balance auto-renewal, switching it off,
// and the renewal price and source shown in the customer's service list.
func TestAutoRenewIntegration(t *testing.T) {
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
	portal := NewPortalStore(db)
	lifecycle := NewLifecycleStore(db)

	var regionID string
	if err := db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.CreatePlan(ctx, Plan{Code: "P1", Name: "Small", ProviderType: "hatch", Virtualization: "podman", VCPU: 1, RAMMB: 64, DiskGB: 1,
		AssignNAT: true, PortMappingCount: 1, DefaultTemplateID: "alpine", AllowedTemplateIDs: []string{"alpine", "debian12"}, Enabled: true,
		Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 300}}})
	if err != nil {
		t.Fatal(err)
	}
	buyer, err := auth.RegisterCustomer(ctx, "buyer@example.com", "Buyer", "hash", true)
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
		paid, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: number, EventType: "payment.succeeded", ProviderTransactionID: number, InvoiceNumber: number, AmountMinor: 300, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", "")
		if err != nil {
			t.Fatal(err)
		}
		id := paid.ServiceIDs[0]
		if _, err := db.Exec(ctx, `UPDATE services SET status='active', next_due_at=now()+interval '12 hours' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	renewing, manual := buy(), buy()
	if _, err := db.Exec(ctx, `UPDATE accounts SET balance_minor=1000 WHERE id=$1`, buyer.AccountID); err != nil {
		t.Fatal(err)
	}

	// Instances renew from the balance by default; switched off, they wait
	// for the customer.
	services, err := portal.ListServices(ctx, buyer.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if !service.AutoRenew || service.Source != "platform" || service.ViaTrade || service.TemplateID != "debian12" ||
			service.RenewalPriceMinor == nil || *service.RenewalPriceMinor != 300 || service.BillingCycle != "monthly" {
			t.Fatalf("listed service = %+v", service)
		}
	}
	if err := portal.SetAutoRenew(ctx, buyer.AccountID, manual, false); err != nil {
		t.Fatal(err)
	}
	if err := portal.SetServiceTemplate(ctx, buyer.AccountID, renewing, "alpine"); err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if err := db.QueryRow(ctx, `SELECT next_due_at FROM services WHERE id=$1`, renewing).Scan(&due); err != nil {
		t.Fatal(err)
	}
	result, err := lifecycle.Run(ctx, 7*24*time.Hour, 72*time.Hour, 168*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result.RenewalInvoices != 2 || result.AutoRenewed != 1 {
		t.Fatalf("lifecycle = %+v", result)
	}
	var status string
	var next time.Time
	if err := db.QueryRow(ctx, `SELECT i.status, s.next_due_at FROM invoices i JOIN services s ON s.id=i.service_id WHERE i.service_id=$1 AND i.kind='renewal'`, renewing).Scan(&status, &next); err != nil || status != "paid" || !next.After(due) {
		t.Fatalf("auto-renewed invoice %s, due %s -> %s: %v", status, due, next, err)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM invoices WHERE service_id=$1 AND kind='renewal'`, manual).Scan(&status); err != nil || status != "open" {
		t.Fatalf("manual renewal paid anyway: %s %v", status, err)
	}
	var balance int64
	if err := db.QueryRow(ctx, `SELECT balance_minor FROM accounts WHERE id=$1`, buyer.AccountID).Scan(&balance); err != nil || balance != 700 {
		t.Fatalf("balance = %d %v", balance, err)
	}
	services, _ = portal.ListServices(ctx, buyer.AccountID)
	for _, service := range services {
		if service.ID == renewing && service.TemplateID != "alpine" {
			t.Fatalf("reinstalled template not shown: %s", service.TemplateID)
		}
	}
	// A balance that does not cover the renewal leaves it for the customer.
	if err := portal.SetAutoRenew(ctx, buyer.AccountID, manual, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts SET balance_minor=100 WHERE id=$1`, buyer.AccountID); err != nil {
		t.Fatal(err)
	}
	if result, err = lifecycle.Run(ctx, 7*24*time.Hour, 72*time.Hour, 168*time.Hour); err != nil || result.AutoRenewed != 0 {
		t.Fatalf("renewed without enough balance: %+v %v", result, err)
	}
}
