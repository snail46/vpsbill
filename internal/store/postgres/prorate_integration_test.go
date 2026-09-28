package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/clock"
)

// TestHostedLeaseProrationIntegration covers a host lease shorter than the
// billing cycle: the buyer pays pro rata, the service ends with the lease,
// renewals wait for the host to extend it and are cut to the lease again.
func TestHostedLeaseProrationIntegration(t *testing.T) {
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
	market := NewMarketplaceStore(db)
	lifecycle := NewLifecycleStore(db)
	newCustomer := func(name string) (accountID, userID string) {
		account, err := billing.CreateAccount(ctx, Account{Kind: "individual", DisplayName: name, BillingEmail: strings.ToLower(name) + "@example.com", CountryCode: "CN", DefaultCurrency: "CNY"})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,email_verified_at,status) VALUES($1,$2,'x',now(),'active') RETURNING id`, strings.ToLower(name)+"@example.com", name).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO memberships(account_id,user_id,role) VALUES($1,$2,'owner')`, account.ID, userID); err != nil {
			t.Fatal(err)
		}
		return account.ID, userID
	}
	hostID, hostUser := newCustomer("Host")
	buyerID, buyerUser := newCustomer("Buyer")
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}

	// The lease ends two months from today (at the end of that date).
	now := time.Now().In(clock.Zone)
	expires := addMonthsClamped(now, 2)
	input := HostedNodeInput{Name: "hk-lease", RegionID: regionID, Location: "香港", LineDescription: "测试线路", ExpiresAt: expires.Format("2006-01-02")}
	if message := input.Validate(); message != "" {
		t.Fatal(message)
	}
	nodeID, err := market.CreateHostedNode(ctx, hostID, hostUser, input, HostedNodeRegistration{BaseURL: "agent://lease", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}, Capacity: map[string]any{}, CapacityVCPU: 4, CapacityRAMMB: 4096, CapacityDiskGB: 50})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.CreatePlan(ctx, Plan{Code: "H-LEASE", Name: "Lease", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, AssignNAT: true, PortMappingCount: 2,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, OwnerAccountID: hostID, NodeID: nodeID,
		Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 1000}, {Currency: "CNY", BillingCycle: "quarterly", AmountMinor: 3000}, {Currency: "CNY", BillingCycle: "d10", AmountMinor: 500}}})
	if err != nil {
		t.Fatal(err)
	}
	topup, err := billing.CreateTopupInvoice(ctx, buyerID, buyerUser, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: "topup-lease", EventType: "payment.succeeded", ProviderTransactionID: "topup-lease", InvoiceNumber: topup.Number, AmountMinor: 20000, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", ""); err != nil {
		t.Fatal(err)
	}
	order := func(cycle string) Order {
		t.Helper()
		created, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: buyerID, ActorType: "customer", Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: cycle, Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
		if err != nil {
			t.Fatalf("order %s: %v", cycle, err)
		}
		return created
	}
	lease := *LeaseEnd(&expires)

	// Cycles inside the lease cost the list price; the quarter is cut to the
	// lease (two months and the rest of today, a little over two thirds).
	if monthly := order("monthly"); monthly.TotalMinor != 1000 {
		t.Fatalf("monthly total = %d", monthly.TotalMinor)
	}
	if days := order("d10"); days.TotalMinor != 500 {
		t.Fatalf("10-day total = %d", days.TotalMinor)
	}
	quarter := order("quarterly")
	if quarter.TotalMinor < 2000 || quarter.TotalMinor > 2040 {
		t.Fatalf("prorated quarter total = %d", quarter.TotalMinor)
	}
	listing, err := market.HostedNodes(ctx, "", true, false)
	if err != nil || len(listing) != 1 {
		t.Fatalf("listing: %v", err)
	}
	listing[0].QuoteLease(time.Now())
	for _, price := range listing[0].Plans[0].Prices {
		prorated := price.ChargeMinor != nil
		if prorated != (price.BillingCycle == "quarterly") || (prorated && *price.ChargeMinor != quarter.TotalMinor) {
			t.Fatalf("market quote for %s = %v", price.BillingCycle, price.ChargeMinor)
		}
	}
	result, err := billing.PayInvoiceWithBalance(ctx, buyerID, quarter.InvoiceID, buyerUser)
	if err != nil || len(result.ServiceIDs) != 1 {
		t.Fatalf("pay: %+v %v", result, err)
	}
	serviceID := result.ServiceIDs[0]
	var due, escrowEnd time.Time
	var locked int64
	if err = db.QueryRow(ctx, `SELECT s.next_due_at,s.renewal_price_minor,e.period_end FROM services s JOIN marketplace_escrows e ON e.service_id=s.id WHERE s.id=$1`, serviceID).Scan(&due, &locked, &escrowEnd); err != nil {
		t.Fatal(err)
	}
	if !due.Equal(lease) || !escrowEnd.Equal(lease) || locked != 3000 {
		t.Fatalf("due=%v escrow end=%v lease=%v locked=%d", due, escrowEnd, lease, locked)
	}

	// At the lease end nothing can be renewed until the host extends it.
	if _, err = db.Exec(ctx, `UPDATE services SET status='active',auto_renew=false,next_due_at=now()+interval '1 hour' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE nodes SET expires_at=(now() AT TIME ZONE 'Asia/Shanghai')::date WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	renewals := func() (count int, total int64) {
		t.Helper()
		if _, err := lifecycle.Run(ctx, 7*24*time.Hour, 72*time.Hour, 168*time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(ctx, `SELECT count(*),coalesce(max(total_minor),0) FROM invoices WHERE service_id=$1 AND kind='renewal'`, serviceID).Scan(&count, &total); err != nil {
			t.Fatal(err)
		}
		return count, total
	}
	if count, _ := renewals(); count != 0 {
		t.Fatal("renewed past the host lease")
	}
	// Extended by about a month and a half: the renewal is cut to the new
	// lease at the locked quarterly price.
	if _, err = db.Exec(ctx, `UPDATE nodes SET expires_at=(now() AT TIME ZONE 'Asia/Shanghai')::date + 45 WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	count, total := renewals()
	if count != 1 || total <= 1000 || total >= 2000 {
		t.Fatalf("prorated renewal: count=%d total=%d", count, total)
	}
	var periodEnd time.Time
	if err = db.QueryRow(ctx, `SELECT period_end FROM invoices WHERE service_id=$1 AND kind='renewal'`, serviceID).Scan(&periodEnd); err != nil {
		t.Fatal(err)
	}
	var nodeExpires time.Time
	if err = db.QueryRow(ctx, `SELECT expires_at FROM nodes WHERE id=$1`, nodeID).Scan(&nodeExpires); err != nil {
		t.Fatal(err)
	}
	if !periodEnd.Equal(*LeaseEnd(&nodeExpires)) {
		t.Fatalf("renewal period end %v, lease %v", periodEnd, LeaseEnd(&nodeExpires))
	}

	// The host did not extend the lease: at the due date the service ends
	// with it (suspended, deleted after the retention period).
	if _, err = db.Exec(ctx, `UPDATE invoices SET status='void' WHERE service_id=$1 AND kind='renewal'`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE nodes SET expires_at=(now() AT TIME ZONE 'Asia/Shanghai')::date - 1 WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE services SET next_due_at=((now() AT TIME ZONE 'Asia/Shanghai')::date)::timestamptz WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	result2, err := lifecycle.Run(ctx, 7*24*time.Hour, 72*time.Hour, 168*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var scheduled *time.Time
	if err = db.QueryRow(ctx, `SELECT status,termination_scheduled_at FROM services WHERE id=$1`, serviceID).Scan(&status, &scheduled); err != nil {
		t.Fatal(err)
	}
	if result2.LeaseEnded != 1 || status != "suspended" || scheduled == nil {
		t.Fatalf("lease end: %+v status=%s scheduled=%v", result2, status, scheduled)
	}
}
