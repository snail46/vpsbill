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

// TestPlanStockIntegration covers stock ceilings, sold-out plans, per-price
// purchase limits and renewals after a cycle stops being sold.
func TestPlanStockIntegration(t *testing.T) {
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
	billing := NewBillingStore(db)
	catalog := NewCatalogStore(db)
	lifecycle := NewLifecycleStore(db)
	account, err := billing.CreateAccount(ctx, Account{Kind: "individual", DisplayName: "Buyer", BillingEmail: "buyer@example.com", CountryCode: "CN", DefaultCurrency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	// One machine with two agents (as on the test box): its hardware counts
	// once. Sellable: 4 cores, 4096 MB, 50 GB.
	for _, name := range []string{"n1", "n2"} {
		if _, err = db.Exec(ctx, `INSERT INTO nodes(region_id,name,provider_type,base_url,api_key_ciphertext,status,virtualization_types,capacity,capacity_vcpu,capacity_ram_mb,capacity_disk_gb,machine_id)
			VALUES($1,$2,'hatch','agent://'||$2,'x','online','{lxc,podman}','{}',4,4096,50,'machine-1')`, regionID, name); err != nil {
			t.Fatal(err)
		}
	}
	three := 3
	newPlan := func(code string, vcpu, ram, disk int, stock *int, prices []Price) Plan {
		t.Helper()
		plan := Plan{Code: code, Name: code, ProviderType: "hatch", Virtualization: "lxc", VCPU: vcpu, RAMMB: ram, DiskGB: disk, AssignNAT: true, PortMappingCount: 1,
			DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, StockLimit: stock, Prices: prices}
		created, err := catalog.CreatePlan(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	two := 2
	small := newPlan("SMALL", 1, 512, 5, &three, []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 500}, {Currency: "CNY", BillingCycle: "d7", AmountMinor: 200, PurchaseLimit: &two}})

	// The small plan holds 3 x (1 core, 512 MB, 5 GB); a 1-core 1 GB 10 GB
	// plan fits once more by cores (4 - 3).
	capacity, err := catalog.PlanStockCapacity(ctx, Plan{ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 1024, DiskGB: 10}, "")
	if err != nil || capacity.Max != 1 || capacity.Nodes != 2 || capacity.FreeVCPU != 1 {
		t.Fatalf("draft capacity = %+v err=%v", capacity, err)
	}
	// Its own ceiling ignores its own stock: 4 cores fit 4 small plans.
	if own, err := catalog.PlanStockCapacity(ctx, small, small.ID); err != nil || own.Max != 4 {
		t.Fatalf("own capacity = %+v err=%v", own, err)
	}

	order := func(cycle string, quantity int) (Order, error) {
		return billing.CreateOrder(ctx, CreateOrderInput{AccountID: account.ID, ActorType: "customer", Items: []OrderItemInput{{PlanID: small.ID, RegionID: regionID, BillingCycle: cycle, Quantity: quantity, Configuration: map[string]any{"template_id": "debian12"}}}})
	}
	var rule *HostedOrderError
	// The 7-day price sells twice.
	if _, err = order("d7", 2); err != nil {
		t.Fatal(err)
	}
	if _, err = order("d7", 1); !errors.As(err, &rule) || !strings.Contains(rule.Message, "限购") {
		t.Fatalf("third 7-day unit: %v", err)
	}
	// Stock 3: two units are held by the unpaid orders, one is left.
	if _, err = order("monthly", 2); !errors.As(err, &rule) || !strings.Contains(rule.Message, "仅剩 1 台") {
		t.Fatalf("over stock: %v", err)
	}
	monthly, err := order("monthly", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = order("monthly", 1); !errors.As(err, &rule) || rule.Message != "该套餐已售罄" {
		t.Fatalf("sold out: %v", err)
	}
	plans, err := catalog.ListPlans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.ID != small.ID {
			continue
		}
		if plan.StockHeld != 3 || plan.StockLimit == nil || *plan.StockLimit != 3 {
			t.Fatalf("listed stock: held=%d limit=%v", plan.StockHeld, plan.StockLimit)
		}
		for _, price := range plan.Prices {
			if price.BillingCycle == "d7" && (price.Sold != 2 || price.PurchaseLimit == nil || *price.PurchaseLimit != 2) {
				t.Fatalf("7-day sold=%d limit=%v", price.Sold, price.PurchaseLimit)
			}
		}
	}

	// Pay the monthly order, then stop selling monthly: the renewal uses
	// the price the service was bought at instead of stopping for review.
	topup, err := billing.CreateTopupInvoice(ctx, account.ID, "", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: "stock-topup", EventType: "payment.succeeded", ProviderTransactionID: "stock-topup", InvoiceNumber: topup.Number, AmountMinor: 10000, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", ""); err != nil {
		t.Fatal(err)
	}
	paid, err := billing.PayInvoiceWithBalance(ctx, account.ID, monthly.InvoiceID, "")
	if err != nil || len(paid.ServiceIDs) != 1 {
		t.Fatalf("pay: %+v %v", paid, err)
	}
	serviceID := paid.ServiceIDs[0]
	small.Prices = []Price{{Currency: "CNY", BillingCycle: "quarterly", AmountMinor: 1200}}
	if _, err = catalog.UpdatePlan(ctx, small.ID, small); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE services SET status='active',auto_renew=false,next_due_at=now()+interval '1 hour' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	result, err := lifecycle.Run(ctx, 7*24*time.Hour, 72*time.Hour, 168*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var renewal int64
	if err = db.QueryRow(ctx, `SELECT s.status, coalesce((SELECT i.total_minor FROM invoices i WHERE i.service_id=s.id AND i.kind='renewal'),0) FROM services s WHERE s.id=$1`, serviceID).Scan(&status, &renewal); err != nil {
		t.Fatal(err)
	}
	if result.PricingReview != 0 || status != "active" || renewal != 500 {
		t.Fatalf("renewal after the cycle was removed: %+v status=%s amount=%d", result, status, renewal)
	}
}
