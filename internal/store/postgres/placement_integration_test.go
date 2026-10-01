package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestPlanPlacementIntegration covers how a plan picks a node (listed
// nodes, packing, spreading), the regions and stock that follow from it,
// plan categories, presets and saving plans in a batch.
func TestPlanPlacementIntegration(t *testing.T) {
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

	region := func(code string) string {
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES($1,$1,true) RETURNING id`, code).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	hk, jp := region("HK"), region("JP")
	// Three machines, each selling 4 cores, 4096 MB and 50 GB.
	node := func(regionID, name string) string {
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes(region_id,name,provider_type,base_url,api_key_ciphertext,status,virtualization_types,capacity,capacity_vcpu,capacity_ram_mb,capacity_disk_gb,machine_id)
			VALUES($1,$2,'hatch','agent://'||$2,'x','online','{lxc}','{}',4,4096,50,'machine-'||$2) RETURNING id`, regionID, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := node(hk, "a"), node(hk, "b")
	c := node(jp, "c")

	category, err := catalog.SavePlanCategory(ctx, "", PlanCategory{Name: " 入门 ", Description: "小内存 NAT", SortOrder: 2})
	if err != nil || category.Name != "入门" {
		t.Fatalf("category = %+v err=%v", category, err)
	}
	if _, err := catalog.SavePlanCategory(ctx, "", PlanCategory{Name: "入门"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate category: %v", err)
	}
	if _, err := catalog.SavePlanCategory(ctx, "00000000-0000-0000-0000-000000000000", PlanCategory{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing category: %v", err)
	}

	draft := func(code, selection string, nodes ...string) Plan {
		return Plan{Code: code, Name: code, ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5,
			AssignNAT: true, PortMappingCount: 1, DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true,
			CategoryID: category.ID, NodeSelection: selection, NodeIDs: nodes,
			Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 100}}}
	}

	// A batch is saved whole or not at all.
	if _, err := catalog.CreatePlans(ctx, []Plan{draft("DUP", "pack"), draft("dup", "pack")}, nil); err == nil {
		t.Fatal("duplicate codes in a batch were saved")
	} else if batchErr := (*PlanBatchError)(nil); !errors.As(err, &batchErr) || batchErr.Index != 1 || batchErr.Message != "套餐编码已存在" {
		t.Fatalf("batch error = %v", err)
	}
	refused := 0
	check := func(_ context.Context, stock PlanStockSource, plan Plan, existing string) (string, error) {
		capacity, err := stock.PlanStockCapacity(ctx, plan, existing)
		if err != nil {
			return "", err
		}
		// The first plan of the batch is already saved when the second
		// is checked, so its stock counts.
		if plan.Code == "STOCK2" && capacity.Max != 4 {
			refused++
			return "库存最多", nil
		}
		return "", nil
	}
	four := 4
	stocked := draft("STOCK1", "nodes", a, b)
	stocked.StockLimit = &four
	if _, err := catalog.CreatePlans(ctx, []Plan{stocked, draft("STOCK2", "nodes", a, b)}, check); err != nil || refused != 0 {
		t.Fatalf("stocked batch: %v refused=%d", err, refused)
	}
	if _, err := db.Exec(ctx, `DELETE FROM plan_prices WHERE plan_id IN (SELECT id FROM plans WHERE code LIKE 'STOCK%');DELETE FROM plans WHERE code LIKE 'STOCK%'`); err != nil {
		t.Fatal(err)
	}
	plans, err := catalog.CreatePlans(ctx, []Plan{draft("PACK", "pack"), draft("SPREAD", "spread"), draft("PINNED", "nodes", b), draft("NOLIST", "pack", c)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, spread, pinned, nolist := plans[0], plans[1], plans[2], plans[3]
	if len(nolist.NodeIDs) != 0 || len(pinned.NodeIDs) != 1 {
		t.Fatalf("node lists = %v / %v", nolist.NodeIDs, pinned.NodeIDs)
	}

	listed := func(id string) Plan {
		t.Helper()
		all, err := catalog.ListPlans(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range all {
			if plan.ID == id {
				return plan
			}
		}
		t.Fatalf("plan %s not listed", id)
		return Plan{}
	}
	if got := listed(pinned.ID); got.CategoryID != category.ID || got.NodeSelection != "nodes" || !slices.Equal(got.RegionIDs, []string{hk}) {
		t.Fatalf("pinned plan = category %q selection %q regions %v", got.CategoryID, got.NodeSelection, got.RegionIDs)
	}
	if got := listed(pack.ID); len(got.RegionIDs) != 2 {
		t.Fatalf("pack regions = %v", got.RegionIDs)
	}
	// A pinned plan's stock comes from its nodes only.
	if capacity, err := catalog.PlanStockCapacity(ctx, listed(pinned.ID), pinned.ID); err != nil || capacity.Nodes != 1 || capacity.Max != 4 {
		t.Fatalf("pinned capacity = %+v err=%v", capacity, err)
	}

	buyer, err := auth.RegisterCustomer(ctx, "buyer@example.com", "Buyer", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	order := func(plan Plan, regionID string) (string, error) {
		created, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: buyer.AccountID, Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
		if err != nil {
			return "", err
		}
		var number string
		if err := db.QueryRow(ctx, `SELECT number FROM invoices WHERE id=$1`, created.InvoiceID).Scan(&number); err != nil {
			t.Fatal(err)
		}
		paid, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: number, EventType: "payment.succeeded", ProviderTransactionID: number, InvoiceNumber: number, AmountMinor: 100, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", "")
		if err != nil {
			t.Fatal(err)
		}
		return paid.ServiceIDs[0], nil
	}
	place := func(plan Plan) string {
		t.Helper()
		service, err := order(plan, hk)
		if err != nil {
			t.Fatal(err)
		}
		nodeID, err := provisioning.ReserveNode(ctx, service)
		if err != nil {
			t.Fatal(err)
		}
		return nodeID
	}

	// A pinned plan cannot be ordered where it has no node.
	var rule *HostedOrderError
	if _, err := order(pinned, jp); !errors.As(err, &rule) || !strings.Contains(rule.Message, "暂无可用节点") {
		t.Fatalf("pinned plan ordered in JP: %v", err)
	}
	// Pinned: always b, which leaves b fuller than a.
	if got := place(pinned); got != b {
		t.Fatalf("pinned placed on %s, want b", got)
	}
	// Pack: the fullest node that fits (b); spread: the emptiest (a).
	if got := place(pack); got != b {
		t.Fatalf("pack placed on %s, want b", got)
	}
	if got := place(spread); got != a {
		t.Fatalf("spread placed on %s, want a", got)
	}
	candidates, err := func() ([]PlacementCandidate, error) {
		service, err := order(pinned, hk)
		if err != nil {
			return nil, err
		}
		return provisioning.PlacementCandidates(ctx, service)
	}()
	if err != nil || len(candidates) != 1 || candidates[0].NodeID != b {
		t.Fatalf("pinned candidates = %+v err=%v", candidates, err)
	}

	// Deleting a category leaves its plans uncategorised.
	categories, err := catalog.ListPlanCategories(ctx)
	if err != nil || len(categories) != 1 || categories[0].Plans != 4 {
		t.Fatalf("categories = %+v err=%v", categories, err)
	}
	if err := catalog.DeletePlanCategory(ctx, category.ID); err != nil {
		t.Fatal(err)
	}
	if got := listed(pack.ID); got.CategoryID != "" {
		t.Fatalf("plan kept deleted category %q", got.CategoryID)
	}

	preset, err := catalog.SavePlanPreset(ctx, "", PlanPreset{Name: "NAT 系列", Settings: json.RawMessage(`{"provider_type":"hatch"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SavePlanPreset(ctx, preset.ID, PlanPreset{Name: "NAT 系列", Settings: json.RawMessage(`{"provider_type":"clicd"}`)}); err != nil {
		t.Fatal(err)
	}
	presets, err := catalog.ListPlanPresets(ctx)
	if err != nil || len(presets) != 1 || !strings.Contains(string(presets[0].Settings), "clicd") {
		t.Fatalf("presets = %+v err=%v", presets, err)
	}
	if err := catalog.DeletePlanPreset(ctx, preset.ID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.DeletePlanPreset(ctx, preset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
