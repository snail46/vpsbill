package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsbill/internal/security"
)

func TestBillingLifecycleIntegration(t *testing.T) {
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
	// A second startup must be harmless; production runs migrations on every boot.
	if err = Migrate(ctx, db); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	var migrationCount int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount < 14 {
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
	plan.AssignNAT = true
	plan.PortMappingCount = 6
	plan.AssignIPv6 = false
	plan, err = NewCatalogStore(db).UpdatePlan(ctx, plan.ID, plan)
	if err != nil || plan.Version != 2 {
		t.Fatalf("update plan: version=%d err=%v", plan.Version, err)
	}
	order, err := billing.CreateOrder(ctx, CreateOrderInput{AccountID: account.ID, ActorType: "system", Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian-bookworm"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var orderConfiguration []byte
	if err = db.QueryRow(ctx, `SELECT configuration FROM order_items WHERE order_id=$1`, order.ID).Scan(&orderConfiguration); err != nil {
		t.Fatal(err)
	}
	var configuration map[string]any
	if err = json.Unmarshal(orderConfiguration, &configuration); err != nil || configuration["assign_nat"] != true || configuration["assign_ipv6"] != false || configuration["port_mapping_count"] != float64(6) {
		t.Fatalf("plan network policy was not snapshotted: %#v err=%v", configuration, err)
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
	candidates, err := provisioning.PlacementCandidates(ctx, serviceID)
	if err != nil || len(candidates) != 1 || candidates[0].NodeID != nodeID || candidates[0].ProviderType != "clicd" {
		t.Fatalf("placement candidates: %+v err=%v", candidates, err)
	}
	if _, err = provisioning.ReserveNode(ctx, serviceID, nodeID); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("excluded node was reserved: err=%v", err)
	}
	reservedNodeID, err := provisioning.ReserveNode(ctx, serviceID)
	if err != nil || reservedNodeID != nodeID {
		t.Fatalf("reserve node: id=%s err=%v", reservedNodeID, err)
	}
	provisionContext, err := provisioning.ProvisionContext(ctx, provisionJob.ID)
	if err != nil || provisionContext.NodeID != nodeID || provisionContext.InstanceName == "" {
		t.Fatalf("provision context: %+v err=%v", provisionContext, err)
	}
	if err = provisioning.CompleteProvision(ctx, provisionJob.ID, "integration-worker", "101", "container-uuid", "192.0.2.10", "2001:db8::10", "running", nil); err != nil {
		t.Fatal(err)
	}
	rootPasswordCiphertext, err := testSecretBox.Seal("InitialRoot123")
	if err != nil {
		t.Fatal(err)
	}
	portal := NewPortalStore(db)
	if err = portal.SaveRootPassword(ctx, account.ID, serviceID, rootPasswordCiphertext); err != nil {
		t.Fatal(err)
	}
	access, err := portal.ServiceAccess(ctx, account.ID, serviceID)
	if err != nil || access.InstanceName == "" || access.PortMappingCount != 6 || len(access.RootPasswordCiphertext) == 0 {
		t.Fatalf("service access=%+v err=%v", access, err)
	}
	if _, err = portal.ServiceAccess(ctx, "00000000-0000-0000-0000-000000000000", serviceID); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("cross-account service lookup should be hidden, got %v", err)
	}
	operations := NewOperationsStore(db)
	ticket, err := operations.CreateTicket(ctx, account.ID, customerID, serviceID, "Integration support request", "high", "Please verify this VPS.", "203.0.113.5", "integration-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = operations.ReplyTicket(ctx, ticket.Ticket.ID, "", "staff", admin.UserID, "Verified.", false); err != nil {
		t.Fatal(err)
	}
	// Image attachments: customers reach their own, never internal notes'.
	image := AttachmentUpload{FileName: "shot.png", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nfake")}
	withImage, err := operations.ReplyTicket(ctx, ticket.Ticket.ID, account.ID, "customer", customerID, "", false, image)
	if err != nil || withImage.Body != attachmentPlaceholder || len(withImage.Attachments) != 1 {
		t.Fatalf("image-only reply: %+v err=%v", withImage, err)
	}
	internalNote, err := operations.ReplyTicket(ctx, ticket.Ticket.ID, "", "staff", admin.UserID, "internal screenshot", true, image)
	if err != nil {
		t.Fatal(err)
	}
	if _, data, attachmentErr := operations.TicketAttachmentData(ctx, ticket.Ticket.ID, withImage.Attachments[0].ID, account.ID); attachmentErr != nil || string(data) != string(image.Data) {
		t.Fatalf("customer attachment: %q err=%v", data, attachmentErr)
	}
	if _, _, attachmentErr := operations.TicketAttachmentData(ctx, ticket.Ticket.ID, internalNote.Attachments[0].ID, account.ID); !errors.Is(attachmentErr, ErrAttachmentNotFound) {
		t.Fatalf("internal attachment must be hidden from customers, got %v", attachmentErr)
	}
	if _, _, attachmentErr := operations.TicketAttachmentData(ctx, ticket.Ticket.ID, withImage.Attachments[0].ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(attachmentErr, ErrAttachmentNotFound) {
		t.Fatalf("other accounts must not read attachments, got %v", attachmentErr)
	}
	if _, _, attachmentErr := operations.TicketAttachmentData(ctx, ticket.Ticket.ID, internalNote.Attachments[0].ID, ""); attachmentErr != nil {
		t.Fatalf("staff read internal attachment: %v", attachmentErr)
	}
	if detail, detailErr := operations.TicketDetail(ctx, ticket.Ticket.ID, account.ID, false); detailErr != nil || len(detail.Messages) != 3 || len(detail.Messages[2].Attachments) != 1 {
		t.Fatalf("customer detail with attachments: %+v err=%v", detail.Messages, detailErr)
	}
	if _, err = db.Exec(ctx, `DELETE FROM support_messages WHERE id IN ($1,$2)`, withImage.ID, internalNote.ID); err != nil {
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
	actionJobID, err := portal.QueueServiceAction(ctx, account.ID, customerID, serviceID, "stop", "203.0.113.5", "integration-test")
	if err != nil || actionJobID == "" {
		t.Fatalf("queue customer action: id=%s err=%v", actionJobID, err)
	}
	actionJob, claimed, err := provisioning.ClaimJob(ctx, "integration-worker")
	if err != nil || !claimed || actionJob.ID != actionJobID || actionJob.Action != "stop" {
		t.Fatalf("claim customer action: claimed=%v job=%+v err=%v", claimed, actionJob, err)
	}
	if err = provisioning.CompleteAction(ctx, actionJob.ID, "integration-worker", "clicd-task-1", ""); err != nil {
		t.Fatal(err)
	}
	// Overdue suspension on pausing nodes records "suspended" as the target,
	// and a live read of the paused instance clears it.
	if _, err = db.Exec(ctx, `UPDATE services SET desired_runtime_status='suspended' WHERE id=$1`, serviceID); err != nil {
		t.Fatalf("desired suspended rejected: %v", err)
	}
	if err = portal.ObserveRuntime(ctx, serviceID, "suspended"); err != nil {
		t.Fatal(err)
	}
	var runtimeStatus, desired string
	if err = db.QueryRow(ctx, `SELECT runtime_status,coalesce(desired_runtime_status,'') FROM services WHERE id=$1`, serviceID).Scan(&runtimeStatus, &desired); err != nil || runtimeStatus != "suspended" || desired != "" {
		t.Fatalf("observe runtime: runtime=%q desired=%q err=%v", runtimeStatus, desired, err)
	}
	if _, err = db.Exec(ctx, `UPDATE services SET runtime_status='running' WHERE id=$1`, serviceID); err != nil {
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

	// Staff can terminate a service immediately; the worker then deletes it.
	terminateID, err := provisioning.QueueAdminServiceAction(ctx, "staff-1", serviceID, "terminate", "203.0.113.9", "integration-test")
	if err != nil || terminateID == "" {
		t.Fatalf("queue admin terminate: id=%s err=%v", terminateID, err)
	}
	if _, err = provisioning.QueueAdminServiceAction(ctx, "staff-1", serviceID, "terminate", "", ""); !errors.Is(err, ErrServiceActionUnavailable) {
		t.Fatalf("second terminate must be rejected, got %v", err)
	}
	terminateJob, claimed, err := provisioning.ClaimJob(ctx, "integration-worker")
	if err != nil || !claimed || terminateJob.ID != terminateID || terminateJob.Action != "terminate" {
		t.Fatalf("claim admin terminate: claimed=%v job=%+v err=%v", claimed, terminateJob, err)
	}
	if err = provisioning.CompleteTermination(ctx, terminateJob.ID, "integration-worker"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT status FROM services WHERE id=$1`, serviceID).Scan(&serviceStatus); err != nil || serviceStatus != "terminated" {
		t.Fatalf("admin terminate: status=%q err=%v", serviceStatus, err)
	}

	// A node that only hosted terminated services can be removed.
	catalog := NewCatalogStore(db)
	if err = catalog.UpdateNode(ctx, nodeID, UpdateNode{Name: "renamed-node", BaseURL: "https://node.example.test/", VirtualizationTypes: []string{"lxc"}, CapacityVCPU: 4, CapacityRAMMB: 4096, CapacityDiskGB: 500}); err != nil {
		t.Fatalf("update node: %v", err)
	}
	var nodeName string
	var keyKept bool
	if err = db.QueryRow(ctx, `SELECT name, api_key_ciphertext=$2 FROM nodes WHERE id=$1`, nodeID, testNodeAPIKey).Scan(&nodeName, &keyKept); err != nil || nodeName != "renamed-node" || !keyKept {
		t.Fatalf("updated node: name=%q keyKept=%v err=%v", nodeName, keyKept, err)
	}
	// Host billing reminders read the expiry, quota and this month's traffic.
	if err = catalog.UpdateNodeBilling(ctx, nodeID, "2026-12-31", 1000); err != nil {
		t.Fatal(err)
	}
	mailStore := NewMailStore(db)
	if _, err = db.Exec(ctx, `UPDATE services SET node_id=$2 WHERE id=$1`, serviceID, nodeID); err != nil {
		t.Fatal(err)
	}
	if err = mailStore.RecordServiceTraffic(ctx, serviceID, 5<<30, nil, nil); err != nil {
		t.Fatal(err)
	}
	watches, err := mailStore.NodeWatches(ctx)
	if err != nil || len(watches) != 1 || watches[0].TrafficQuotaGB != 1000 || watches[0].UsedBytes != 5<<30 || watches[0].ExpiresAt == nil || watches[0].ExpiresAt.UTC().Format("2006-01-02") != "2026-12-31" {
		t.Fatalf("node watches: %+v err=%v", watches, err)
	}
	if _, err = db.Exec(ctx, `UPDATE services SET node_id=NULL WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	// Mail queue: dedup, lease, retry and completion.
	if added, queueErr := mailStore.EnqueueMail(ctx, "ops@example.com", "subject", "body", "dedup-1"); queueErr != nil || !added {
		t.Fatalf("enqueue: added=%v err=%v", added, queueErr)
	}
	if added, queueErr := mailStore.EnqueueMail(ctx, "ops@example.com", "subject", "body", "dedup-1"); queueErr != nil || added {
		t.Fatalf("duplicate enqueue: added=%v err=%v", added, queueErr)
	}
	queued, err := mailStore.ClaimMail(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].Attempts != 1 {
		t.Fatalf("claim: %+v err=%v", queued, err)
	}
	if again, againErr := mailStore.ClaimMail(ctx, 10); againErr != nil || len(again) != 0 {
		t.Fatalf("leased mail queued twice: %+v err=%v", again, againErr)
	}
	if err = mailStore.FailMail(ctx, queued[0].ID, 1, 8, 0, "smtp down"); err != nil {
		t.Fatal(err)
	}
	if retried, retryErr := mailStore.ClaimMail(ctx, 10); retryErr != nil || len(retried) != 1 || retried[0].Attempts != 2 {
		t.Fatalf("retry claim: %+v err=%v", retried, retryErr)
	}
	if err = mailStore.CompleteMail(ctx, queued[0].ID); err != nil {
		t.Fatal(err)
	}
	if emails, emailErr := mailStore.StaffEmails(ctx); emailErr != nil || len(emails) != 1 || emails[0] != "admin@example.com" {
		t.Fatalf("staff emails: %v err=%v", emails, emailErr)
	}
	if err = catalog.DeleteNode(ctx, nodeID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	if err = catalog.DeleteNode(ctx, nodeID); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("second delete must report not found, got %v", err)
	}

	// Changing a password keeps the current session and ends the others.
	if err = auth.CreateSession(ctx, admin.UserID, []byte("keep-token"), []byte("csrf-1"), time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if err = auth.CreateSession(ctx, admin.UserID, []byte("other-token"), []byte("csrf-2"), time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if err = auth.ChangePassword(ctx, admin.UserID, "new-password-hash", []byte("keep-token")); err != nil {
		t.Fatal(err)
	}
	if hash, hashErr := auth.PasswordHash(ctx, admin.UserID); hashErr != nil || hash != "new-password-hash" {
		t.Fatalf("password hash=%q err=%v", hash, hashErr)
	}
	if _, err = auth.SessionByToken(ctx, []byte("keep-token")); err != nil {
		t.Fatalf("current session must survive: %v", err)
	}
	if _, err = auth.SessionByToken(ctx, []byte("other-token")); err == nil {
		t.Fatal("other sessions must be revoked")
	}

	// Reset links: only the newest one works, once, and it ends every session.
	ownerID, ownerEmail, err := auth.AccountOwnerLogin(ctx, account.ID)
	if err != nil || ownerID != customerID || ownerEmail != "customer@example.com" {
		t.Fatalf("account owner: id=%s email=%s err=%v", ownerID, ownerEmail, err)
	}
	if err = auth.CreatePasswordReset(ctx, customerID, []byte("reset-old"), "self", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = auth.CreatePasswordReset(ctx, customerID, []byte("reset-new"), "staff", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if recent, recentErr := auth.RecentPasswordResets(ctx, customerID); recentErr != nil || recent != 1 {
		t.Fatalf("recent self resets=%d err=%v", recent, recentErr)
	}
	if err = auth.CreateSession(ctx, customerID, []byte("customer-token"), []byte("csrf-3"), time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.ResetPassword(ctx, []byte("reset-old"), "reset-hash"); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("superseded link must fail, got %v", err)
	}
	if resetUser, resetErr := auth.ResetPassword(ctx, []byte("reset-new"), "reset-hash"); resetErr != nil || resetUser != customerID {
		t.Fatalf("reset: user=%s err=%v", resetUser, resetErr)
	}
	if _, err = auth.ResetPassword(ctx, []byte("reset-new"), "again"); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("used link must fail, got %v", err)
	}
	if hash, hashErr := auth.PasswordHash(ctx, customerID); hashErr != nil || hash != "reset-hash" {
		t.Fatalf("customer hash=%q err=%v", hash, hashErr)
	}
	var customerSessions int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM login_sessions WHERE user_id=$1`, customerID).Scan(&customerSessions); err != nil || customerSessions != 0 {
		t.Fatalf("sessions after reset=%d err=%v", customerSessions, err)
	}
}
