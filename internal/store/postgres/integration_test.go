package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"clicd-billing/internal/security"
)

func TestBillingLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	if !strings.Contains(databaseURL, "clicd_billing_test") {
		t.Fatal("refusing to reset database without clicd_billing_test in TEST_DATABASE_URL")
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
	// A second startup must be harmless; production runs migrations on every boot.
	if err = Migrate(ctx, db); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	var migrationCount int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount < 12 {
		t.Fatalf("only %d migrations applied", migrationCount)
	}
	billing := NewBillingStore(db)
	account, err := billing.CreateAccount(ctx, Account{Kind: "individual", DisplayName: "Integration Customer", BillingEmail: "integration@example.com", CountryCode: "CN", DefaultCurrency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAuthStore(db)
	admin, err := auth.BootstrapAdmin(ctx, "admin@example.com", "Integration Admin", "not-a-real-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	var customerID string
	if err = db.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,email_verified_at,status) VALUES('customer@example.com','Integration Customer','not-a-real-password-hash',now(),'active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO memberships(account_id,user_id,role) VALUES($1,$2,'owner')`, account.ID, customerID); err != nil {
		t.Fatal(err)
	}
	if err = auth.CreateSession(ctx, customerID, []byte("integration-session"), []byte("integration-csrf"), time.Now().Add(time.Hour), "203.0.113.5", "integration-test"); err != nil {
		t.Fatal(err)
	}
	if err = billing.UpdateAccountStatus(ctx, account.ID, "suspended", admin.UserID); err != nil {
		t.Fatal(err)
	}
	var sessions int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM login_sessions WHERE user_id=$1`, customerID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("suspension did not revoke sessions: count=%d err=%v", sessions, err)
	}
	if err = billing.UpdateAccountStatus(ctx, account.ID, "active", admin.UserID); err != nil {
		t.Fatal(err)
	}
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('TST','Test Region',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	plan, err := NewCatalogStore(db).CreatePlan(ctx, Plan{Code: "TEST-LXC", Name: "Test LXC", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 10, TrafficGB: 100, NetworkDownMbps: 100, NetworkUpMbps: 100, SnapshotLimit: 1, DefaultTemplateID: "debian-bookworm", AllowedTemplateIDs: []string{"debian-bookworm"}, Enabled: true, Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 1900}}})
	if err != nil {
		t.Fatal(err)
	}
	order, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: account.ID, ActorType: "system", Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian-bookworm", "assign_nat": true}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "test", ProviderEventID: "evt-initial", EventType: "payment.succeeded", ProviderTransactionID: "tx-initial", InvoiceNumber: order.InvoiceNumber, AmountMinor: 1900, Currency: "CNY", Payload: json.RawMessage(`{"test":true}`)}, "system", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.ServiceIDs) != 1 {
		t.Fatalf("services=%v", initial.ServiceIDs)
	}
	replayed, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "test", ProviderEventID: "evt-initial", EventType: "payment.succeeded", ProviderTransactionID: "tx-initial", InvoiceNumber: order.InvoiceNumber, AmountMinor: 1900, Currency: "CNY", Payload: json.RawMessage(`{"test":true}`)}, "system", "")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Duplicate || replayed.TransactionID != "" || len(replayed.ServiceIDs) != 0 {
		t.Fatalf("payment replay was not ignored: %+v", replayed)
	}
	serviceID := initial.ServiceIDs[0]
	testEncryptionKey := strings.Repeat("0123456789abcdef", 4)
	testSecretBox, err := security.NewSecretBox(testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	testNodeAPIKey, err := testSecretBox.Seal("integration-node-api-key")
	if err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err = db.QueryRow(ctx, `INSERT INTO nodes(region_id,name,base_url,api_key_ciphertext,status,virtualization_types,capacity_vcpu,capacity_ram_mb,capacity_disk_gb) VALUES($1,'integration-node','https://node.example.test',$2,'online',ARRAY['lxc'],8,8192,1000) RETURNING id`, regionID, testNodeAPIKey).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	provisioning := NewProvisioningStore(db)
	provisionJob, claimed, err := provisioning.ClaimJob(ctx, "integration-worker")
	if err != nil || !claimed || provisionJob.ServiceID != serviceID || provisionJob.Action != "provision" {
		t.Fatalf("claim provision job: claimed=%v job=%+v err=%v", claimed, provisionJob, err)
	}
	reservedNodeID, err := provisioning.ReserveNode(ctx, serviceID)
	if err != nil || reservedNodeID != nodeID {
		t.Fatalf("reserve node: id=%s err=%v", reservedNodeID, err)
	}
	provisionContext, err := provisioning.ProvisionContext(ctx, provisionJob.ID)
	if err != nil || provisionContext.NodeID != nodeID || provisionContext.InstanceName == "" {
		t.Fatalf("provision context: %+v err=%v", provisionContext, err)
	}
	if err = provisioning.CompleteProvision(ctx, provisionJob.ID, "integration-worker", "101", "container-uuid", "192.0.2.10", "2001:db8::10", "running"); err != nil {
		t.Fatal(err)
	}
	operations := NewOperationsStore(db)
	ticket, err := operations.CreateTicket(ctx, account.ID, customerID, serviceID, "Integration support request", "high", "Please verify this VPS.", "203.0.113.5", "integration-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = operations.ReplyTicket(ctx, ticket.Ticket.ID, "", "staff", admin.UserID, "Verified.", false); err != nil {
		t.Fatal(err)
	}
	if err = operations.UpdateTicketStatus(ctx, ticket.Ticket.ID, admin.UserID, "resolved"); err != nil {
		t.Fatal(err)
	}
	ticketDetail, err := operations.TicketDetail(ctx, ticket.Ticket.ID, account.ID, false)
	if err != nil || ticketDetail.Ticket.Status != "resolved" || len(ticketDetail.Messages) != 2 {
		t.Fatalf("ticket lifecycle: %+v err=%v", ticketDetail, err)
	}
	if _, err = db.Exec(ctx, `UPDATE services SET next_due_at=now()-interval '4 days',expires_at=now()-interval '4 days' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	lifecycle := NewLifecycleStore(db)
	results := make(chan LifecycleResult, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, runErr := lifecycle.Run(ctx, 7*24*time.Hour, 3*24*time.Hour, 7*24*time.Hour)
			results <- value
			errorsFound <- runErr
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for runErr := range errorsFound {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	var result LifecycleResult
	for value := range results {
		result.RenewalInvoices += value.RenewalInvoices
		result.Overdue += value.Overdue
		result.Suspended += value.Suspended
	}
	if result.RenewalInvoices != 1 || result.Overdue != 1 || result.Suspended != 1 {
		t.Fatalf("unexpected concurrent lifecycle result %+v", result)
	}
	var renewalNumber, status string
	var renewalAmount int64
	if err = db.QueryRow(ctx, `SELECT number,status,total_minor FROM invoices WHERE service_id=$1 AND kind='renewal'`, serviceID).Scan(&renewalNumber, &status, &renewalAmount); err != nil {
		t.Fatal(err)
	}
	if status != "open" || renewalAmount != 1900 {
		t.Fatalf("renewal status=%s amount=%d", status, renewalAmount)
	}
	var overdueStopStatus string
	if err = db.QueryRow(ctx, `SELECT status FROM provisioning_jobs WHERE service_id=$1 AND action='stop' AND payload->>'source'='billing_lifecycle'`, serviceID).Scan(&overdueStopStatus); err != nil || overdueStopStatus != "pending" {
		t.Fatalf("overdue stop status=%q err=%v", overdueStopStatus, err)
	}
	if _, err = billing.ProcessPayment(ctx, PaymentEvent{Provider: "test", ProviderEventID: "evt-renewal", EventType: "payment.succeeded", ProviderTransactionID: "tx-renewal", InvoiceNumber: renewalNumber, AmountMinor: renewalAmount, Currency: "CNY", Payload: json.RawMessage(`{"test":true}`)}, "system", ""); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT status FROM provisioning_jobs WHERE service_id=$1 AND action='stop' AND payload->>'source'='billing_lifecycle'`, serviceID).Scan(&overdueStopStatus); err != nil || overdueStopStatus != "dead" {
		t.Fatalf("paid service stop was not cancelled: status=%q err=%v", overdueStopStatus, err)
	}
	var serviceStatus string
	var nextDue time.Time
	if err = db.QueryRow(ctx, `SELECT status,next_due_at FROM services WHERE id=$1`, serviceID).Scan(&serviceStatus, &nextDue); err != nil {
		t.Fatal(err)
	}
	if serviceStatus != "active" || !nextDue.After(time.Now()) {
		t.Fatalf("service status=%s next_due=%s", serviceStatus, nextDue)
	}
	portal := NewPortalStore(db)
	actionJobID, err := portal.QueueServiceAction(ctx, account.ID, customerID, serviceID, "stop", "203.0.113.5", "integration-test")
	if err != nil || actionJobID == "" {
		t.Fatalf("queue customer action: id=%s err=%v", actionJobID, err)
	}
	actionJob, claimed, err := provisioning.ClaimJob(ctx, "integration-worker")
	if err != nil || !claimed || actionJob.ID != actionJobID || actionJob.Action != "stop" {
		t.Fatalf("claim customer action: claimed=%v job=%+v err=%v", claimed, actionJob, err)
	}
	if err = provisioning.CompleteAction(ctx, actionJob.ID, "integration-worker", "clicd-task-1"); err != nil {
		t.Fatal(err)
	}
	var transactions int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE account_id=$1`, account.ID).Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if transactions != 2 {
		t.Fatalf("transactions=%d", transactions)
	}
	monitoring := NewMonitoringStore(db)
	overview, err := monitoring.Overview(ctx)
	if err != nil || overview.Accounts != 1 || overview.Nodes != 1 || overview.CapacityVCPU != 8 || len(overview.Revenue30Days) != 1 {
		t.Fatalf("operations overview: %+v err=%v", overview, err)
	}
	hosts, err := monitoring.Hosts(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].ProviderType != "clicd" || hosts[0].ReservedVCPU != 1 {
		t.Fatalf("host probes: %+v err=%v", hosts, err)
	}
}
