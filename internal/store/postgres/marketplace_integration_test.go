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

func TestHostingMarketplaceIntegration(t *testing.T) {
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
	operations := NewOperationsStore(db)
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
	strangerID, _ := newCustomer("Stranger")
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	balanceOf := func(accountID string) int64 {
		wallet, err := billing.Wallet(ctx, accountID, 10)
		if err != nil {
			t.Fatal(err)
		}
		return wallet.BalanceMinor
	}

	// Publish a node and a plan for it.
	input := HostedNodeInput{Name: "hk-host-1", RegionID: regionID, Location: "香港 葵涌", LineDescription: "CN2 GIA 回程", ExpiresAt: time.Now().AddDate(1, 0, 0).Format("2006-01-02"), TrafficQuotaGB: 2000}
	if message := input.Validate(); message != "" {
		t.Fatal(message)
	}
	nodeID, err := market.CreateHostedNode(ctx, hostID, hostUser, input, HostedNodeRegistration{BaseURL: "agent://integration", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}, Capacity: map[string]any{"runtimes": []string{"lxc"}}, CapacityVCPU: 4, CapacityRAMMB: 4096, CapacityDiskGB: 50})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = market.CreateHostedNode(ctx, hostID, hostUser, input, HostedNodeRegistration{BaseURL: "agent://other", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}}); !errors.Is(err, ErrNodeNameTaken) {
		t.Fatalf("duplicate node name: %v", err)
	}
	plan, err := catalog.CreatePlan(ctx, Plan{Code: "H-TEST", Name: "HK Small", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, TrafficGB: 200, AssignNAT: true, PortMappingCount: 5,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, OwnerAccountID: hostID, NodeID: nodeID, Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 3000}}})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := catalog.ListPlans(ctx)
	if err != nil || len(PlatformPlans(plans)) != 0 || plans[0].NodeID != nodeID || plans[0].OwnerAccountID != hostID {
		t.Fatalf("hosted plan is not marked: %+v err=%v", plans, err)
	}
	listed, err := market.HostedNodes(ctx, "", true, false)
	if err != nil || len(listed) != 1 || len(listed[0].Plans) != 1 || listed[0].OwnerEmail != "" || listed[0].FreeVCPU != 4 {
		t.Fatalf("market listing: %+v err=%v", listed, err)
	}

	// Top up, then pay a hosted order from the balance.
	topup, err := billing.CreateTopupInvoice(ctx, buyerID, buyerUser, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = billing.CreateTopupInvoice(ctx, buyerID, buyerUser, 50); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("tiny top-up accepted: %v", err)
	}
	if _, err = billing.PayInvoiceWithBalance(ctx, buyerID, topup.ID, buyerUser); !errors.Is(err, ErrInvoiceUnavailable) {
		t.Fatalf("top-up paid from balance: %v", err)
	}
	paid, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: "topup-1", EventType: "payment.succeeded", ProviderTransactionID: "topup-1", InvoiceNumber: topup.Number, AmountMinor: 10000, Currency: "CNY", Payload: json.RawMessage(`{}`)}, "staff", "")
	if err != nil || !paid.Topup || balanceOf(buyerID) != 10000 {
		t.Fatalf("top-up: %+v balance=%d err=%v", paid, balanceOf(buyerID), err)
	}
	order := func(accountID string, quantity int) (Order, error) {
		return billing.CreateOrder(ctx, CreateOrderInput{AccountID: accountID, ActorType: "customer", Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: quantity, Configuration: map[string]any{"template_id": "debian12"}}}})
	}
	var hostedErr *HostedOrderError
	if _, err = order(hostID, 1); !errors.As(err, &hostedErr) {
		t.Fatalf("host bought its own plan: %v", err)
	}
	if _, err = order(buyerID, 5); !errors.As(err, &hostedErr) {
		t.Fatalf("order beyond node capacity accepted: %v", err)
	}
	big, err := order(buyerID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = billing.PayInvoiceWithBalance(ctx, buyerID, big.InvoiceID, buyerUser); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("overdraft allowed: %v", err)
	}
	if _, err = billing.PayInvoiceWithBalance(ctx, strangerID, big.InvoiceID, buyerUser); !errors.Is(err, ErrInvoiceUnavailable) {
		t.Fatalf("paid someone else's invoice: %v", err)
	}
	small, err := order(buyerID, 1)
	if err != nil {
		t.Fatal(err)
	}
	result, err := billing.PayInvoiceWithBalance(ctx, buyerID, small.InvoiceID, buyerUser)
	if err != nil || len(result.ServiceIDs) != 1 || balanceOf(buyerID) != 7000 {
		t.Fatalf("balance payment: %+v balance=%d err=%v", result, balanceOf(buyerID), err)
	}
	if _, err = billing.PayInvoiceWithBalance(ctx, buyerID, small.InvoiceID, buyerUser); !errors.Is(err, ErrInvoiceUnavailable) {
		t.Fatalf("invoice paid twice: %v", err)
	}
	serviceID := result.ServiceIDs[0]
	var gross, fee, share int64
	var start, end time.Time
	if err = db.QueryRow(ctx, `SELECT gross_minor,fee_minor,host_share_minor,period_start,period_end FROM marketplace_escrows WHERE service_id=$1`, serviceID).Scan(&gross, &fee, &share, &start, &end); err != nil {
		t.Fatal(err)
	}
	if gross != 3000 || fee != 600 || share != 2400 {
		t.Fatalf("escrow split: gross=%d fee=%d share=%d", gross, fee, share)
	}

	// Scheduling stays on the host's node.
	candidates, err := NewProvisioningStore(db).PlacementCandidates(ctx, serviceID)
	if err != nil || len(candidates) != 1 || candidates[0].NodeID != nodeID {
		t.Fatalf("placement candidates: %+v err=%v", candidates, err)
	}

	// Nothing is released until the service works; then day by day.
	if released, err := market.ReleaseEscrows(ctx, start.Add(10*24*time.Hour+time.Hour)); err != nil || released != 0 {
		t.Fatalf("released before provisioning: %d err=%v", released, err)
	}
	if _, err = db.Exec(ctx, `UPDATE services SET status='active' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	at := start.Add(10*24*time.Hour + time.Hour)
	total, elapsed := EscrowDays(start, end, at)
	if elapsed != 10 {
		t.Fatalf("elapsed days = %d", elapsed)
	}
	for range 2 {
		if _, err := market.ReleaseEscrows(ctx, at); err != nil {
			t.Fatal(err)
		}
	}
	earned := share * 10 / int64(total)
	if balanceOf(hostID) != earned {
		t.Fatalf("host earnings = %d, want %d", balanceOf(hostID), earned)
	}

	// Tickets on the hosted instance reach the host first.
	ticket, err := operations.CreateTicket(ctx, buyerID, buyerUser, serviceID, "Network is slow", "normal", "ping loss", "203.0.113.9", "test")
	if err != nil || ticket.Ticket.HostAccountID != hostID {
		t.Fatalf("hosted ticket: %+v err=%v", ticket.Ticket, err)
	}
	hostTickets, err := operations.ListHostTickets(ctx, hostID)
	if err != nil || len(hostTickets) != 1 || hostTickets[0].HostName != "Host" {
		t.Fatalf("host tickets: %+v err=%v", hostTickets, err)
	}
	if _, err = operations.HostTicketDetail(ctx, ticket.Ticket.ID, strangerID); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("stranger read hosted ticket: %v", err)
	}
	if _, err = operations.ReplyTicket(ctx, ticket.Ticket.ID, strangerID, "host", buyerUser, "hi", false); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("stranger replied as host: %v", err)
	}
	if _, err = operations.ReplyTicket(ctx, ticket.Ticket.ID, "", "host", hostUser, "hi", false); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("host reply without account scope: %v", err)
	}
	if _, err = operations.ReplyTicket(ctx, ticket.Ticket.ID, hostID, "host", hostUser, "Rebooting the uplink", false); err != nil {
		t.Fatal(err)
	}
	detail, err := operations.TicketDetail(ctx, ticket.Ticket.ID, buyerID, false)
	if err != nil || len(detail.Messages) != 2 || detail.Messages[1].AuthorType != "host" {
		t.Fatalf("buyer view of host reply: %+v err=%v", detail.Messages, err)
	}

	// The chat room holds the host and the buyer.
	for account, want := range map[string]string{hostID: "host", buyerID: "buyer", strangerID: ""} {
		role, retired, err := market.ChatRole(ctx, nodeID, account)
		if err != nil || role != want || retired {
			t.Fatalf("chat role of %s = %q retired=%v err=%v, want %q", account, role, retired, err, want)
		}
	}
	if _, _, err = market.ChatRole(ctx, nodeID, ""); err != nil {
		t.Fatalf("staff chat lookup: %v", err)
	}
	posted, err := market.PostChatMessage(ctx, nodeID, "buyer", buyerUser, buyerID, "Buyer", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !posted.ForViewer(buyerID, buyerUser).Mine || posted.ForViewer(hostID, hostUser).Mine {
		t.Fatal("chat ownership flag is wrong")
	}
	rooms, err := market.ChatRooms(ctx, buyerID)
	if err != nil || len(rooms) != 1 || rooms[0].Members != 2 || rooms[0].LastMessage == nil || rooms[0].LastMessage.Body != "hello" {
		t.Fatalf("buyer rooms: %+v err=%v", rooms, err)
	}
	if rooms, _ := market.ChatRooms(ctx, strangerID); len(rooms) != 0 {
		t.Fatalf("stranger sees rooms: %+v", rooms)
	}
	if rooms, _ := market.ChatRooms(ctx, ""); len(rooms) != 1 || rooms[0].Role != "staff" {
		t.Fatalf("staff rooms: %+v", rooms)
	}

	// Clearance pays the buyer twice the remaining value; the host pays one share.
	var releasedGross int64
	if err = db.QueryRow(ctx, `SELECT released_gross_minor FROM marketplace_escrows WHERE service_id=$1`, serviceID).Scan(&releasedGross); err != nil {
		t.Fatal(err)
	}
	remaining := gross - releasedGross
	cleared, err := market.ClearNode(ctx, nodeID, 2, "离线测试", "system", "")
	if err != nil || len(cleared.Services) != 1 || cleared.Services[0].RemainingMinor != remaining || cleared.RefundMinor != 2*remaining || cleared.PenaltyMinor != remaining {
		t.Fatalf("clearance: %+v err=%v", cleared, err)
	}
	if balanceOf(buyerID) != 7000+2*remaining || balanceOf(hostID) != earned-remaining {
		t.Fatalf("after clearance buyer=%d host=%d", balanceOf(buyerID), balanceOf(hostID))
	}
	var serviceStatus, escrowStatus, listing string
	var planEnabled bool
	if err = db.QueryRow(ctx, `SELECT s.status,e.status,n.listing_status,p.enabled FROM services s JOIN marketplace_escrows e ON e.service_id=s.id JOIN plans p ON p.id=s.plan_id JOIN nodes n ON n.id=p.node_id WHERE s.id=$1`, serviceID).Scan(&serviceStatus, &escrowStatus, &listing, &planEnabled); err != nil {
		t.Fatal(err)
	}
	if serviceStatus != "terminated" || escrowStatus != "cleared" || listing != "retired" || planEnabled {
		t.Fatalf("after clearance service=%s escrow=%s listing=%s plan=%v", serviceStatus, escrowStatus, listing, planEnabled)
	}
	if _, err = market.ClearNode(ctx, nodeID, 2, "again", "system", ""); !errors.Is(err, ErrNodeRetired) {
		t.Fatalf("second clearance: %v", err)
	}
	if role, retired, _ := market.ChatRole(ctx, nodeID, buyerID); role != "" || !retired {
		t.Fatalf("cleared buyer still in room: role=%q retired=%v", role, retired)
	}
	input.Name = "hk-host-2"
	if _, err = market.CreateHostedNode(ctx, hostID, hostUser, input, HostedNodeRegistration{BaseURL: "agent://second", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}}); !errors.Is(err, ErrHostInDebt) {
		t.Fatalf("indebted host published a node: %v", err)
	}

	// Balance history is append-only.
	if _, err = db.Exec(ctx, `UPDATE wallet_entries SET amount_minor=1`); err == nil {
		t.Fatal("wallet entries were modified")
	}
	wallet, err := billing.Wallet(ctx, buyerID, 10)
	if err != nil || len(wallet.Entries) != 3 || wallet.Entries[0].Kind != "clearance_refund" {
		t.Fatalf("buyer wallet: %+v err=%v", wallet, err)
	}
	adjusted, err := billing.AdjustWallet(ctx, hostID, buyerUser, 100000, "结清测试")
	if err != nil || adjusted.BalanceMinor != earned-remaining+100000 {
		t.Fatalf("adjust wallet: %+v err=%v", adjusted, err)
	}
	if _, err = billing.AdjustWallet(ctx, buyerID, buyerUser, -10_000_000, "too much"); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("adjustment overdrew: %v", err)
	}
}
