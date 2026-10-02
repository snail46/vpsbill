package postgres_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
)

// Staff get one of the fixed roles; nobody changes their own access, and a
// super administrator always remains.
func TestStaffRolesIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "staff")
	auth := postgres.NewAuthStore(db)
	admin, err := auth.BootstrapAdmin(ctx, "admin@example.com", "Admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := auth.StaffRoles(ctx)
	if err != nil || len(roles) != 6 || roles[0].Name != postgres.SuperAdministrator || roles[0].Members != 1 || roles[5].Name != "Read Only" {
		t.Fatalf("roles: %+v err=%v", roles, err)
	}
	for _, role := range roles[1:] {
		if slices.Contains(role.Permissions, "*") || slices.Contains(role.Permissions, "staff:manage") || slices.Contains(role.Permissions, "backups:manage") {
			t.Fatalf("role %s may manage staff or backups: %v", role.Name, role.Permissions)
		}
	}

	support, err := auth.CreateStaff(ctx, admin.UserID, "Support@Example.com", " Sue ", "hash-1", "Support")
	if err != nil || support.Email != "support@example.com" || support.DisplayName != "Sue" || support.Role != "Support" || support.Status != "active" {
		t.Fatalf("create staff: %+v err=%v", support, err)
	}
	if _, err = auth.CreateStaff(ctx, admin.UserID, "support@example.com", "Again", "hash", "Support"); !errors.Is(err, postgres.ErrEmailExists) {
		t.Fatalf("duplicate staff: %v", err)
	}
	if _, err = auth.CreateStaff(ctx, admin.UserID, "new@example.com", "New", "hash", "Owner"); !errors.Is(err, postgres.ErrRoleNotFound) {
		t.Fatalf("unknown role: %v", err)
	}
	if _, err = auth.RegisterCustomer(ctx, "customer@example.com", "Customer", "hash", true); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.CreateStaff(ctx, admin.UserID, "customer@example.com", "Customer", "hash", "Support"); !errors.Is(err, postgres.ErrEmailIsCustomer) {
		t.Fatalf("customer made staff: %v", err)
	}

	// A role carries its permissions into the session.
	identity, err := auth.StaffByEmail(ctx, "support@example.com")
	if err != nil || identity.RoleName != "Support" || !slices.Contains(identity.Permissions, "tickets:write") || slices.Contains(identity.Permissions, "settings:write") || slices.Contains(identity.Permissions, "billing:write") {
		t.Fatalf("support identity: %+v err=%v", identity, err)
	}

	finance, disabled := "Finance", "disabled"
	if _, err = auth.UpdateStaff(ctx, admin.UserID, admin.UserID, postgres.StaffChange{Role: &finance}); !errors.Is(err, postgres.ErrStaffSelf) {
		t.Fatalf("changed own role: %v", err)
	}
	if _, err = auth.UpdateStaff(ctx, support.UserID, admin.UserID, postgres.StaffChange{Role: &finance}); !errors.Is(err, postgres.ErrLastSuperAdmin) {
		t.Fatalf("demoted the last super administrator: %v", err)
	}
	if _, err = auth.UpdateStaff(ctx, support.UserID, admin.UserID, postgres.StaffChange{Status: &disabled}); !errors.Is(err, postgres.ErrLastSuperAdmin) {
		t.Fatalf("disabled the last super administrator: %v", err)
	}
	if err = auth.RemoveStaff(ctx, support.UserID, admin.UserID); !errors.Is(err, postgres.ErrLastSuperAdmin) {
		t.Fatalf("removed the last super administrator: %v", err)
	}
	if err = auth.RemoveStaff(ctx, admin.UserID, admin.UserID); !errors.Is(err, postgres.ErrStaffSelf) {
		t.Fatalf("removed oneself: %v", err)
	}
	changed, err := auth.UpdateStaff(ctx, admin.UserID, support.UserID, postgres.StaffChange{Role: &finance})
	if err != nil || changed.Role != "Finance" {
		t.Fatalf("change role: %+v err=%v", changed, err)
	}

	// Disabling signs the member out; so does a password reset, which can
	// also drop the second factor.
	sessions := func() (count int) {
		if err := db.QueryRow(ctx, `SELECT count(*) FROM login_sessions WHERE user_id=$1`, support.UserID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	signIn := func(token string) {
		if err := auth.CreateSession(ctx, support.UserID, []byte(token), []byte("csrf"), time.Now().Add(time.Hour), "", "test"); err != nil {
			t.Fatal(err)
		}
	}
	signIn("one")
	if _, err = db.Exec(ctx, `UPDATE users SET mfa_enabled=true,mfa_secret_encrypted='\x01' WHERE id=$1`, support.UserID); err != nil {
		t.Fatal(err)
	}
	if err = auth.ResetStaffPassword(ctx, admin.UserID, support.UserID, "hash-2", true); err != nil || sessions() != 0 {
		t.Fatalf("reset password: sessions=%d err=%v", sessions(), err)
	}
	if identity, err = auth.StaffByEmail(ctx, "support@example.com"); err != nil || identity.PasswordHash != "hash-2" || identity.MFAEnabled {
		t.Fatalf("after reset: %+v err=%v", identity, err)
	}
	signIn("two")
	if changed, err = auth.UpdateStaff(ctx, admin.UserID, support.UserID, postgres.StaffChange{Status: &disabled}); err != nil || changed.Status != "disabled" || sessions() != 0 {
		t.Fatalf("disable: %+v sessions=%d err=%v", changed, sessions(), err)
	}

	// A removed member can be added again with the same address.
	if err = auth.RemoveStaff(ctx, admin.UserID, support.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.StaffByEmail(ctx, "support@example.com"); !errors.Is(err, postgres.ErrInvalidLogin) {
		t.Fatalf("removed staff still signs in: %v", err)
	}
	back, err := auth.CreateStaff(ctx, admin.UserID, "support@example.com", "Sue", "hash-3", "Read Only")
	if err != nil || back.UserID != support.UserID || back.Status != "active" || back.Role != "Read Only" {
		t.Fatalf("staff added again: %+v err=%v", back, err)
	}
	members, err := auth.StaffMembers(ctx)
	if err != nil || len(members) != 2 || members[0].Role != postgres.SuperAdministrator {
		t.Fatalf("members: %+v err=%v", members, err)
	}
	var audited int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action LIKE 'staff.%' AND actor_id=$1`, admin.UserID).Scan(&audited); err != nil || audited != 6 {
		t.Fatalf("staff changes audited: %d err=%v", audited, err)
	}
}

// The payment gateway takes top-ups and the platform's own products;
// what a host sells is paid from the balance.
func TestGatewayInvoicesIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "gateway")
	auth := postgres.NewAuthStore(db)
	billing := postgres.NewBillingStore(db)
	catalog := postgres.NewCatalogStore(db)
	market := postgres.NewMarketplaceStore(db)
	host, err := auth.RegisterCustomer(ctx, "host@example.com", "Host", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	buyer, err := auth.RegisterCustomer(ctx, "buyer@example.com", "Buyer", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	hostedNode, err := market.CreateHostedNode(ctx, host.AccountID, host.UserID,
		postgres.HostedNodeInput{Name: "hk-host-1", RegionID: regionID, Location: "HK", LineDescription: "CN2", ExpiresAt: time.Now().AddDate(1, 0, 0).Format("2006-01-02"), TrafficQuotaGB: 2000},
		postgres.HostedNodeRegistration{BaseURL: "agent://gateway-test", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}, Capacity: map[string]any{"runtimes": []string{"lxc"}}, CapacityVCPU: 4, CapacityRAMMB: 4096, CapacityDiskGB: 50})
	if err != nil {
		t.Fatal(err)
	}
	plan := postgres.Plan{Name: "Small", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, TrafficGB: 200, AssignNAT: true, PortMappingCount: 5,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, Prices: []postgres.Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 3000}}}
	hosted, own := plan, plan
	hosted.Code, hosted.OwnerAccountID, hosted.NodeID = "HOSTED", host.AccountID, hostedNode
	own.Code = "OWN"
	if hosted, err = catalog.CreatePlan(ctx, hosted); err != nil {
		t.Fatal(err)
	}
	if own, err = catalog.CreatePlan(ctx, own); err != nil {
		t.Fatal(err)
	}
	order := func(planID string) postgres.Order {
		created, err := billing.CreateOrder(ctx, postgres.CreateOrderInput{AccountID: buyer.AccountID, ActorType: "customer", Items: []postgres.OrderItemInput{{PlanID: planID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	hostedOrder, ownOrder := order(hosted.ID), order(own.ID)
	topup, err := billing.CreateTopupInvoice(ctx, buyer.AccountID, buyer.UserID, 5000)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = billing.PreparePaymentIntent(ctx, buyer.AccountID, hostedOrder.InvoiceID, "epay"); !errors.Is(err, postgres.ErrBalanceOnly) {
		t.Fatalf("gateway took a hosted order: %v", err)
	}
	if intent, err := billing.PreparePaymentIntent(ctx, buyer.AccountID, ownOrder.InvoiceID, "epay"); err != nil || intent.AmountMinor != 3000 {
		t.Fatalf("gateway refused a platform order: %+v err=%v", intent, err)
	}
	if intent, err := billing.PreparePaymentIntent(ctx, buyer.AccountID, topup.ID, "epay"); err != nil || intent.AmountMinor != 5000 {
		t.Fatalf("gateway refused a top-up: %+v err=%v", intent, err)
	}
	if _, err = billing.PreparePaymentIntent(ctx, host.AccountID, topup.ID, "epay"); !errors.Is(err, postgres.ErrInvoiceUnavailable) {
		t.Fatalf("someone else's invoice: %v", err)
	}
	invoices, err := postgres.NewPortalStore(db).ListInvoices(ctx, buyer.AccountID)
	if err != nil || len(invoices) != 3 {
		t.Fatalf("invoices: %+v err=%v", invoices, err)
	}
	for _, invoice := range invoices {
		if want := invoice.ID != hostedOrder.InvoiceID; invoice.Gateway != want {
			t.Errorf("invoice %s (%s): gateway=%v, want %v", invoice.Number, invoice.Kind, invoice.Gateway, want)
		}
	}
	admin, err := billing.ListInvoices(ctx, buyer.AccountID)
	if err != nil || len(admin) != 3 {
		t.Fatalf("admin invoices: %+v err=%v", admin, err)
	}
	for _, invoice := range admin {
		if want := invoice.ID != hostedOrder.InvoiceID; invoice.Gateway != want {
			t.Errorf("admin invoice %s: gateway=%v, want %v", invoice.Number, invoice.Gateway, want)
		}
	}
}
