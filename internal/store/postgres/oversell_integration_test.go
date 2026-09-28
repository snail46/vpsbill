package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"vpsbill/internal/provider"
)

// TestOversellIntegration covers sellable capacity from reported hardware
// and oversell ratios, nodes sharing one machine, traffic overselling and
// health holds.
func TestOversellIntegration(t *testing.T) {
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
	provisioning := NewProvisioningStore(db)
	limits := OvercommitLimits{CPU: 4, RAM: 1.5, Disk: 2, Traffic: 3}

	var regionID string
	if err := db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	node := func(name string) string {
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes(region_id,name,provider_type,base_url,api_key_ciphertext,status,virtualization_types,capacity,traffic_quota_gb)
			VALUES($1,$2,'hatch','agent://'||$2,'x','online','{lxc}','{}',150) RETURNING id`, regionID, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	healthy := &provider.HostHealth{CPUs: 4, MemTotalMB: 4096, MemAvailableMB: 2048, Load15: 0.5}
	report := func(id string, health *provider.HostHealth) {
		t.Helper()
		info := provider.HostInfo{Raw: map[string]any{}, MachineID: "machine-1", Health: health,
			Capacity: provider.Capacity{VCPU: 4, RAMMB: 4096, DiskGB: 100}}
		if err := catalog.RecordNodeReport(ctx, id, info); err != nil {
			t.Fatal(err)
		}
	}
	sellable := func(id string) (vcpu, ram, disk int64) {
		t.Helper()
		if err := db.QueryRow(ctx, `SELECT capacity_vcpu,capacity_ram_mb,capacity_disk_gb FROM nodes WHERE id=$1`, id).Scan(&vcpu, &ram, &disk); err != nil {
			t.Fatal(err)
		}
		return vcpu, ram, disk
	}

	// Sellable capacity is the report times the node's ratio; ratios above
	// the platform maximum are refused.
	n1, n2 := node("n1"), node("n2")
	report(n1, healthy)
	if vcpu, ram, disk := sellable(n1); vcpu != 4 || ram != 4096 || disk != 100 {
		t.Fatalf("1x capacity = %d/%d/%d", vcpu, ram, disk)
	}
	if err := catalog.SetOvercommit(ctx, n1, "", Overcommit{CPU: 2, RAM: 1.5, Disk: 1, Traffic: 1}, limits); err != nil {
		t.Fatal(err)
	}
	if vcpu, ram, disk := sellable(n1); vcpu != 8 || ram != 6144 || disk != 100 {
		t.Fatalf("oversold capacity = %d/%d/%d", vcpu, ram, disk)
	}
	if err := catalog.SetOvercommit(ctx, n1, "", Overcommit{CPU: 5, RAM: 1, Disk: 1, Traffic: 1}, limits); !errors.Is(err, ErrOvercommitInvalid) {
		t.Fatalf("ratio above the maximum: %v", err)
	}
	// A staff cap lowers the verified hardware before the ratio applies.
	if _, err := db.Exec(ctx, `UPDATE nodes SET owner_account_id=NULL, capacity_cap_vcpu=2 WHERE id=$1`, n1); err != nil {
		t.Fatal(err)
	}
	report(n1, healthy)
	if vcpu, _, _ := sellable(n1); vcpu != 4 {
		t.Fatalf("capped vCPU = %d, want 2 cores x 2", vcpu)
	}
	if _, err := db.Exec(ctx, `UPDATE nodes SET capacity_cap_vcpu=NULL WHERE id=$1`, n1); err != nil {
		t.Fatal(err)
	}
	report(n1, healthy)

	// A second agent on the same machine shares its reservations.
	report(n2, healthy)
	if err := catalog.SetOvercommit(ctx, n2, "", Overcommit{CPU: 2, RAM: 1.5, Disk: 1, Traffic: 2}, limits); err != nil {
		t.Fatal(err)
	}
	var shared []string
	if err := db.QueryRow(ctx, `SELECT array_agg(name ORDER BY name) FROM nodes WHERE machine_id='machine-1'`).Scan(&shared); err != nil || len(shared) != 2 {
		t.Fatalf("machine group = %v %v", shared, err)
	}

	buyer, err := auth.RegisterCustomer(ctx, "buyer@example.com", "Buyer", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	newPlan := func(code string, ramMB int) Plan {
		plan, err := catalog.CreatePlan(ctx, Plan{Code: code, Name: code, ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: ramMB, DiskGB: 1, TrafficGB: 100,
			AssignNAT: true, PortMappingCount: 1, DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true,
			Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 100}}})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	big, tiny := newPlan("BIG", 4096), newPlan("TINY", 64)
	buy := func(plan Plan) string {
		t.Helper()
		order, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: buyer.AccountID, Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
		if err != nil {
			t.Fatal(err)
		}
		var number string
		if err := db.QueryRow(ctx, `SELECT number FROM invoices WHERE id=$1`, order.InvoiceID).Scan(&number); err != nil {
			t.Fatal(err)
		}
		paid, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: number, EventType: "payment.succeeded", ProviderTransactionID: number, InvoiceNumber: number, AmountMinor: 100, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", "")
		if err != nil {
			t.Fatal(err)
		}
		return paid.ServiceIDs[0]
	}
	// 4096 MB on n1 or n2 leaves 2048 MB of the shared 6144 on either.
	if _, err := provisioning.ReserveNode(ctx, buy(big)); err != nil {
		t.Fatal(err)
	}
	if _, err := provisioning.ReserveNode(ctx, buy(big)); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("second node on the same machine sold its hardware again: %v", err)
	}

	// A sustained overload holds n1; small instances then go to n2.
	lowMemory := &provider.HostHealth{CPUs: 4, MemTotalMB: 4096, MemAvailableMB: 100, Load15: 0.5}
	report(n1, lowMemory)
	if _, err := db.Exec(ctx, `UPDATE nodes SET health_mem_since=now()-interval '31 minutes' WHERE id=$1`, n1); err != nil {
		t.Fatal(err)
	}
	report(n1, lowMemory)
	var hold string
	if err := db.QueryRow(ctx, `SELECT coalesce(health_hold_reason,'') FROM nodes WHERE id=$1`, n1).Scan(&hold); err != nil || !strings.Contains(hold, "可用内存") {
		t.Fatalf("hold = %q %v", hold, err)
	}
	if held, err := NewMailStore(db).HeldNodes(ctx); err != nil || len(held) != 1 || held[0].ID != n1 {
		t.Fatalf("held nodes = %+v %v", held, err)
	}
	// Traffic: 150 GB x 2 on n2 holds three 100 GB plans across the machine.
	for range 2 {
		placed, err := provisioning.ReserveNode(ctx, buy(tiny))
		if err != nil || placed != n2 {
			t.Fatalf("placed on %s (held %s): %v", placed, n1, err)
		}
	}
	if _, err := provisioning.ReserveNode(ctx, buy(tiny)); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("traffic oversold beyond the ratio: %v", err)
	}
	report(n1, healthy)
	if err := db.QueryRow(ctx, `SELECT coalesce(health_hold_reason,'') FROM nodes WHERE id=$1`, n1).Scan(&hold); err != nil || hold != "" {
		t.Fatalf("hold not lifted: %q %v", hold, err)
	}
}
