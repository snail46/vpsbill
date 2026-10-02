package settings_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/config"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/store/postgres/pgtest"
)

// Switching the ledger currency converts every stored amount at the rate,
// after which the site works in the new currency; switching back works
// the same way.
func TestLedgerSwitchIntegration(t *testing.T) {
	ctx := context.Background()
	db := pgtest.Open(t, "ledger")
	box, err := security.NewSecretBox(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := settings.NewManager(ctx, db, box, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := runtime.Install(ctx, settings.InstallInput{AppName: "Test Cloud", PublicURL: "https://cloud.example.com", AdminDisplayName: "Admin", AdminEmail: "admin@example.com", AdminPasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	admin := installed.Identity.UserID
	// 7 CNY for one USD keeps the expected amounts easy to read.
	if err = runtime.SetLocale(ctx, settings.LocaleSettings{DefaultLang: "zh", USDEnabled: false, DefaultCurrency: "CNY", USDRate: 7}, admin); err != nil {
		t.Fatal(err)
	}

	// Every money column must be known to the conversion.
	rows, err := db.Query(ctx, `
		SELECT table_name,column_name FROM information_schema.columns
		WHERE table_schema='public' AND data_type='bigint' AND (column_name LIKE '%\_minor' OR column_name LIKE '%discount_value')
		ORDER BY 1,2`)
	if err != nil {
		t.Fatal(err)
	}
	known := postgres.LedgerAmountColumns()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(known[table], column) {
			t.Errorf("%s.%s holds money but ConvertLedger does not convert it", table, column)
		}
	}
	rows.Close()

	auth := postgres.NewAuthStore(db)
	billing := postgres.NewBillingStore(db)
	catalog := postgres.NewCatalogStore(db)
	market := postgres.NewMarketplaceStore(db)
	host, err := auth.RegisterCustomer(ctx, "host@example.com", "Host", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	buyer, err := auth.RegisterCustomer(ctx, "buyer@example.com", "Buyer", "hash", true)
	if err != nil || buyer.DefaultCurrency != "CNY" {
		t.Fatalf("buyer: %+v err=%v", buyer, err)
	}
	if _, err = billing.AdjustWallet(ctx, buyer.AccountID, admin, 10000, "seed"); err != nil {
		t.Fatal(err)
	}
	var regionID string
	if err = db.QueryRow(ctx, `INSERT INTO regions(code,name,enabled) VALUES('HK','Hong Kong',true) RETURNING id`).Scan(&regionID); err != nil {
		t.Fatal(err)
	}
	nodeID, err := market.CreateHostedNode(ctx, host.AccountID, host.UserID,
		postgres.HostedNodeInput{Name: "hk-host-1", RegionID: regionID, Location: "HK", LineDescription: "CN2", ExpiresAt: time.Now().AddDate(1, 0, 0).Format("2006-01-02"), TrafficQuotaGB: 2000},
		postgres.HostedNodeRegistration{BaseURL: "agent://ledger-test", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}, Capacity: map[string]any{"runtimes": []string{"lxc"}}, CapacityVCPU: 4, CapacityRAMMB: 4096, CapacityDiskGB: 50})
	if err != nil {
		t.Fatal(err)
	}
	base := postgres.Plan{Name: "Small", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, TrafficGB: 200, AssignNAT: true, PortMappingCount: 5,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true}
	hosted, own := base, base
	hosted.Code, hosted.OwnerAccountID, hosted.NodeID = "HOSTED", host.AccountID, nodeID
	// The currency sent with a price is ignored: prices are in the ledger's.
	hosted.Prices = []postgres.Price{{Currency: "EUR", BillingCycle: "monthly", AmountMinor: 3500}}
	own.Code, own.Prices = "OWN", []postgres.Price{{BillingCycle: "monthly", AmountMinor: 2100}}
	if hosted, err = catalog.CreatePlan(ctx, hosted); err != nil || hosted.Prices[0].Currency != "CNY" {
		t.Fatalf("hosted plan: %+v err=%v", hosted.Prices, err)
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
	hostedOrder := order(hosted.ID)
	paid, err := billing.PayInvoiceWithBalance(ctx, buyer.AccountID, hostedOrder.InvoiceID, buyer.UserID)
	if err != nil || len(paid.ServiceIDs) != 1 {
		t.Fatalf("pay hosted order: %+v err=%v", paid, err)
	}
	ownOrder := order(own.ID)
	if _, err = db.Exec(ctx, `INSERT INTO service_listings(service_id,seller_account_id,currency,price_minor) VALUES($1,$2,'CNY',300)`, paid.ServiceIDs[0], buyer.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO coupons(code,discount_type,discount_value) VALUES('SEVEN','amount',700),('TENPCT','percent',10)`); err != nil {
		t.Fatal(err)
	}

	one := func(query string, args ...any) (value int64) {
		t.Helper()
		if err := db.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return value
	}
	text := func(query string, args ...any) (value string) {
		t.Helper()
		if err := db.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return value
	}
	balance := func() int64 { return one(`SELECT balance_minor FROM accounts WHERE id=$1`, buyer.AccountID) }
	if balance() != 6500 {
		t.Fatalf("balance before the switch = %d", balance())
	}
	var gross, fee, share int64
	escrow := func() {
		t.Helper()
		if err := db.QueryRow(ctx, `SELECT gross_minor,fee_minor,host_share_minor FROM marketplace_escrows WHERE service_id=$1`, paid.ServiceIDs[0]).Scan(&gross, &fee, &share); err != nil {
			t.Fatal(err)
		}
	}
	escrow()
	if gross != 3500 || share != gross-fee {
		t.Fatalf("escrow before the switch: gross=%d fee=%d share=%d", gross, fee, share)
	}

	// An online payment under way blocks the switch: its amount was agreed.
	topup, err := billing.CreateTopupInvoice(ctx, buyer.AccountID, buyer.UserID, 1400)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := billing.PreparePaymentIntent(ctx, buyer.AccountID, topup.ID, "epay")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.SwitchLedger(ctx, "USD", admin); !errors.Is(err, postgres.ErrLedgerBusy) {
		t.Fatalf("switched with a payment under way: %v", err)
	}
	if balance() != 6500 || runtime.Current().Locale.Ledger() != "CNY" {
		t.Fatal("a refused switch changed something")
	}
	if _, err = db.Exec(ctx, `UPDATE payment_intents SET expires_at=now()-interval '1 minute' WHERE id=$1`, intent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.SwitchLedger(ctx, "CNY", admin); !errors.Is(err, settings.ErrInvalidSettings) {
		t.Fatalf("switched to the same currency: %v", err)
	}

	counts, err := runtime.SwitchLedger(ctx, "USD", admin)
	if err != nil {
		t.Fatal(err)
	}
	locale := runtime.Current().Locale
	if locale.Ledger() != "USD" || locale.Currency() != "USD" || !locale.USDEnabled || counts.Accounts != 2 || counts.PlanPrices != 2 {
		t.Fatalf("after the switch: locale=%+v counts=%+v", locale, counts)
	}
	// 6500/7 = 928.57, 3500/7 = 500, 2100/7 = 300, 1400/7 = 200.
	if balance() != 929 {
		t.Errorf("balance = %d, want 929", balance())
	}
	for query, want := range map[string]int64{
		`SELECT amount_minor FROM plan_prices WHERE plan_id='` + own.ID + `'`:                                    300,
		`SELECT amount_minor FROM plan_prices WHERE plan_id='` + hosted.ID + `'`:                                 500,
		`SELECT total_minor FROM invoices WHERE id='` + ownOrder.InvoiceID + `'`:                                 300,
		`SELECT balance_minor FROM invoices WHERE id='` + ownOrder.InvoiceID + `'`:                               300,
		`SELECT total_minor FROM orders WHERE id='` + ownOrder.ID + `'`:                                          300,
		`SELECT total_minor FROM invoices WHERE id='` + topup.ID + `'`:                                           200,
		`SELECT total_minor FROM invoices WHERE id='` + hostedOrder.InvoiceID + `'`:                              500,
		`SELECT balance_minor FROM invoices WHERE id='` + hostedOrder.InvoiceID + `'`:                            0,
		`SELECT amount_minor FROM transactions WHERE invoice_id='` + hostedOrder.InvoiceID + `'`:                 500,
		`SELECT discount_value FROM coupons WHERE code='SEVEN'`:                                                  100,
		`SELECT discount_value FROM coupons WHERE code='TENPCT'`:                                                 10,
		`SELECT price_minor FROM service_listings`:                                                               100,
		`SELECT count(*) FROM wallet_entries WHERE currency<>'USD'`:                                              0,
		`SELECT count(*) FROM invoices WHERE currency<>'USD'`:                                                    0,
		`SELECT count(*) FROM accounts WHERE default_currency<>'USD'`:                                            0,
		`SELECT count(*) FROM plan_prices WHERE currency<>'USD'`:                                                 0,
		`SELECT count(*) FROM payment_intents WHERE status IN ('pending','redirected')`:                          0,
		`SELECT amount_minor FROM wallet_entries WHERE kind='payment'`:                                           -500,
		`SELECT balance_after_minor FROM wallet_entries WHERE kind='payment'`:                                    929,
		`SELECT count(*) FROM services WHERE list_price_minor IS NOT NULL AND list_price_minor<>500`:             0,
		`SELECT count(*) FROM order_items WHERE unit_amount_minor NOT IN (300,500)`:                              0,
		`SELECT count(*) FROM invoices WHERE subtotal_minor+tax_minor<>total_minor OR balance_minor>total_minor`: 0,
	} {
		if got := one(query); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
	escrow()
	if gross != 500 || share != gross-fee || share <= 0 {
		t.Errorf("escrow after the switch: gross=%d fee=%d share=%d", gross, fee, share)
	}

	// The site goes on in USD.
	fresh, err := auth.RegisterCustomer(ctx, "new@example.com", "New", "hash", true)
	if err != nil || fresh.DefaultCurrency != "USD" {
		t.Fatalf("customer after the switch: %+v err=%v", fresh, err)
	}
	if result, err := billing.PayInvoiceWithBalance(ctx, buyer.AccountID, ownOrder.InvoiceID, buyer.UserID); err != nil || len(result.ServiceIDs) != 1 || balance() != 629 {
		t.Fatalf("pay in USD: %+v balance=%d err=%v", result, balance(), err)
	}
	if got := text(`SELECT currency FROM wallet_entries ORDER BY created_at DESC, id LIMIT 1`); got != "USD" {
		t.Errorf("new wallet entry is in %s", got)
	}
	second := order(own.ID)
	if second.Currency != "USD" || second.TotalMinor != 300 {
		t.Fatalf("order after the switch: %+v", second)
	}
	// Epay takes CNY: the intent keeps the invoice amount and what is charged.
	intent, err = billing.PreparePaymentIntent(ctx, buyer.AccountID, second.InvoiceID, "epay")
	if err != nil || intent.Currency != "USD" || intent.AmountMinor != 300 {
		t.Fatalf("intent in USD: %+v err=%v", intent, err)
	}
	charge := locale.Convert(intent.AmountMinor, intent.Currency, "CNY")
	if err = billing.SetPaymentIntentCheckoutURL(ctx, intent.ID, buyer.AccountID, "https://pay.example.com/x", "CNY", charge); err != nil {
		t.Fatal(err)
	}
	stored, err := billing.PaymentIntentByMerchantReference(ctx, intent.MerchantReference, "epay")
	if amount, currency := stored.Charged(); err != nil || amount != 2100 || currency != "CNY" || stored.AmountMinor != 300 {
		t.Fatalf("stored intent: %+v err=%v", stored, err)
	}
	if _, err = billing.ProcessPayment(ctx, postgres.PaymentEvent{Provider: "epay", ProviderEventID: "t1", EventType: "payment.succeeded", ProviderTransactionID: "t1", InvoiceNumber: stored.InvoiceNumber, AmountMinor: stored.AmountMinor, Currency: stored.Currency, Payload: []byte(`{}`)}, "payment_provider", "epay"); err != nil {
		t.Fatalf("gateway payment of a USD invoice: %v", err)
	}

	// And back to CNY.
	if _, err = runtime.SwitchLedger(ctx, "CNY", admin); err != nil {
		t.Fatal(err)
	}
	if locale = runtime.Current().Locale; locale.Ledger() != "CNY" || locale.Currency() != "CNY" || locale.CNYHidden {
		t.Fatalf("after switching back: %+v", locale)
	}
	for query, want := range map[string]int64{
		`SELECT balance_minor FROM accounts WHERE id='` + buyer.AccountID + `'`:  4403,
		`SELECT amount_minor FROM plan_prices WHERE plan_id='` + own.ID + `'`:    2100,
		`SELECT amount_minor FROM plan_prices WHERE plan_id='` + hosted.ID + `'`: 3500,
		`SELECT discount_value FROM coupons WHERE code='SEVEN'`:                  700,
		`SELECT count(*) FROM accounts WHERE default_currency<>'CNY'`:            0,
		`SELECT count(*) FROM wallet_entries WHERE currency<>'CNY'`:              0,
		`SELECT count(*) FROM ledger_switches`:                                   2,
	} {
		if got := one(query); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
	state, err := runtime.LedgerState(ctx)
	if err != nil || len(state.History) != 2 || state.History[0].To != "CNY" || state.History[1].Counts.Accounts != 2 {
		t.Fatalf("ledger state: %+v err=%v", state, err)
	}
}
