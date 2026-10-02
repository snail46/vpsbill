package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// hostedTermsAndCoupons runs after TestHostingMarketplaceIntegration has set
// up accounts: purchase limits, both coupon lines, recurring discounts on
// renewal and buyer refunds.
func hostedTermsAndCoupons(t *testing.T, ctx context.Context, db *pgxpool.Pool, hostID, hostUser, buyerID, buyerUser, strangerID, regionID string) {
	t.Helper()
	billing := NewBillingStore(db)
	catalog := NewCatalogStore(db)
	market := NewMarketplaceStore(db)
	coupons := NewCouponStore(db)
	balanceOf := func(accountID string) int64 {
		wallet, err := billing.Wallet(ctx, accountID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return wallet.BalanceMinor
	}
	if _, err := billing.AdjustWallet(ctx, buyerID, buyerUser, 100000, "测试充值"); err != nil {
		t.Fatal(err)
	}
	if _, err := billing.AdjustWallet(ctx, strangerID, buyerUser, 100000, "测试充值"); err != nil {
		t.Fatal(err)
	}

	input := HostedNodeInput{Name: "hk-host-3", RegionID: regionID, Location: "香港", LineDescription: "BGP", ExpiresAt: time.Now().AddDate(1, 0, 0).Format("2006-01-02")}
	nodeID, err := market.CreateHostedNode(ctx, hostID, hostUser, input, HostedNodeRegistration{BaseURL: "agent://third", APIKeyCiphertext: []byte("sealed"), VirtualizationTypes: []string{"lxc"}, CapacityVCPU: 8, CapacityRAMMB: 8192, CapacityDiskGB: 100})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.CreatePlan(ctx, Plan{Code: "H-TERMS", Name: "Limited", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 5, AssignNAT: true, PortMappingCount: 5,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, OwnerAccountID: hostID, NodeID: nodeID,
		Description: "适合建站", PurchaseLimit: 1, EarlyRefund: true, Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 3000}}})
	if err != nil {
		t.Fatal(err)
	}
	plans, _ := catalog.ListPlans(ctx)
	if plans[0].ID != plan.ID || plans[0].Description != "适合建站" || plans[0].PurchaseLimit != 1 || !plans[0].EarlyRefund {
		t.Fatalf("plan terms not stored: %+v", plans[0])
	}
	var platformPlanID string
	if err := db.QueryRow(ctx, `INSERT INTO plans(code,name,virtualization,vcpu,ram_mb,disk_gb,default_template_id,allowed_template_ids,enabled,provider_type) VALUES('P-1','Platform','lxc',1,512,5,'debian12','{debian12}',true,'hatch') RETURNING id`).Scan(&platformPlanID); err != nil {
		t.Fatal(err)
	}

	// Host coupons only cover the host's plans; platform coupons only
	// platform plans; a host cannot claim another host's plans.
	hostCoupon := CouponInput{Code: "host20", DiscountType: "percent", DiscountValue: 20, MaxUses: 1, Recurring: true, Enabled: true, PlanIDs: []string{plan.ID}}
	if message := hostCoupon.Validate(); message != "" || hostCoupon.Code != "HOST20" {
		t.Fatalf("coupon validation: %q %q", message, hostCoupon.Code)
	}
	if _, err := coupons.CreateCoupon(ctx, hostID, hostCoupon); err != nil {
		t.Fatal(err)
	}
	if _, err := coupons.CreateCoupon(ctx, hostID, hostCoupon); !errors.Is(err, ErrCouponCodeTaken) {
		t.Fatalf("duplicate code: %v", err)
	}
	var rule *HostedOrderError
	if _, err := coupons.CreateCoupon(ctx, hostID, CouponInput{Code: "STEAL", DiscountType: "amount", DiscountValue: 100, Enabled: true, PlanIDs: []string{platformPlanID}}); !errors.As(err, &rule) {
		t.Fatalf("host coupon for a platform plan: %v", err)
	}
	if _, err := coupons.CreateCoupon(ctx, "", CouponInput{Code: "PLAT5", DiscountType: "amount", DiscountValue: 500, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if _, err := coupons.CreateCoupon(ctx, hostID, CouponInput{Code: "OLD", DiscountType: "percent", DiscountValue: 50, Enabled: true, ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	quote, err := coupons.Quote(ctx, "host20", buyerID, plan.ID, "monthly")
	if err != nil || quote.DiscountMinor != 600 || quote.FinalMinor != 2400 || !quote.Recurring {
		t.Fatalf("quote: %+v err=%v", quote, err)
	}
	if q, err := coupons.Quote(ctx, "PLAT5", buyerID, platformPlanID, "monthly"); err == nil {
		t.Fatalf("quote for a plan without a price: %+v", q)
	}
	order := func(accountID, code string) (Order, error) {
		return billing.CreateOrder(ctx, CreateOrderInput{AccountID: accountID, ActorType: "customer", CouponCode: code, Items: []OrderItemInput{{PlanID: plan.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: 1, Configuration: map[string]any{"template_id": "debian12"}}}})
	}
	for code, want := range map[string]string{"PLAT5": "优惠码不适用于所选套餐", "OLD": "优惠码已过期", "NOPE": "优惠码无效"} {
		if _, err := order(buyerID, code); !errors.As(err, &rule) || rule.Message != want {
			t.Fatalf("coupon %s: %v", code, err)
		}
	}
	if _, err := order(hostID, "HOST20"); !errors.As(err, &rule) {
		t.Fatalf("host used own coupon: %v", err)
	}

	// A per-account limit counts each buyer's discounted instances, unpaid
	// orders included, while other buyers keep their own allowance.
	multi, err := catalog.CreatePlan(ctx, Plan{Code: "H-MULTI", Name: "Multi", ProviderType: "hatch", Virtualization: "lxc", VCPU: 1, RAMMB: 256, DiskGB: 2, AssignNAT: true, PortMappingCount: 2,
		DefaultTemplateID: "debian12", AllowedTemplateIDs: []string{"debian12"}, Enabled: true, OwnerAccountID: hostID, NodeID: nodeID,
		Prices: []Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	perAccount := CouponInput{Code: "ONEEACH", DiscountType: "amount", DiscountValue: 100, MaxUses: 5, PerAccountLimit: 1, Enabled: true, PlanIDs: []string{multi.ID}}
	if bad := (CouponInput{Code: "TOOMANY", DiscountType: "amount", DiscountValue: 100, MaxUses: 1, PerAccountLimit: 2}); bad.Validate() == "" {
		t.Fatal("per-account limit above max uses accepted")
	}
	if _, err := coupons.CreateCoupon(ctx, hostID, perAccount); err != nil {
		t.Fatal(err)
	}
	orderMulti := func(accountID string, quantity int) (Order, error) {
		return billing.CreateOrder(ctx, CreateOrderInput{AccountID: accountID, ActorType: "customer", CouponCode: "ONEEACH", Items: []OrderItemInput{{PlanID: multi.ID, RegionID: regionID, BillingCycle: "monthly", Quantity: quantity, Configuration: map[string]any{"template_id": "debian12"}}}})
	}
	if _, err := orderMulti(buyerID, 2); !errors.As(err, &rule) || rule.Message != "该优惠码每个账号限用 1 次，你还能用 1 次" {
		t.Fatalf("two units over a per-account limit of one: %v", err)
	}
	first, err := orderMulti(buyerID, 1)
	if err != nil || first.DiscountMinor != 100 {
		t.Fatalf("first per-account use: %+v err=%v", first, err)
	}
	if _, err := orderMulti(buyerID, 1); !errors.As(err, &rule) || rule.Message != "该优惠码每个账号限用 1 次，你已用完" {
		t.Fatalf("second per-account use: %v", err)
	}
	if other, err := orderMulti(strangerID, 1); err != nil || other.DiscountMinor != 100 {
		t.Fatalf("another account's use: %+v err=%v", other, err)
	}
	listed, err := coupons.ListCoupons(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range listed {
		if c.Code == "ONEEACH" && (c.PerAccountLimit != 1 || c.UsedCount != 2) {
			t.Fatalf("listed per-account coupon: %+v", c)
		}
	}
	// Free the unpaid orders so the purchase checks below start clean.
	if _, err := db.Exec(ctx, `UPDATE invoices SET status='void' WHERE order_id IN ($1::uuid,(SELECT o.id FROM orders o WHERE o.account_id=$2 AND o.coupon_id IS NOT NULL ORDER BY o.created_at DESC LIMIT 1))`, first.ID, strangerID); err != nil {
		t.Fatal(err)
	}

	discounted, err := order(buyerID, "HOST20")
	if err != nil || discounted.SubtotalMinor != 3000 || discounted.DiscountMinor != 600 || discounted.TotalMinor != 2400 {
		t.Fatalf("discounted order: %+v err=%v", discounted, err)
	}
	// One use is left for nobody, and the plan limit stops a second instance.
	if _, err := order(strangerID, "HOST20"); !errors.As(err, &rule) || rule.Message != "优惠码已达到使用次数上限" {
		t.Fatalf("coupon over max uses: %v", err)
	}
	if _, err := order(buyerID, ""); !errors.As(err, &rule) {
		t.Fatalf("purchase limit not enforced on unpaid order: %v", err)
	}
	paid, err := billing.PayInvoiceWithBalance(ctx, buyerID, discounted.InvoiceID, buyerUser)
	if err != nil || len(paid.ServiceIDs) != 1 {
		t.Fatalf("pay discounted order: %+v err=%v", paid, err)
	}
	serviceID := paid.ServiceIDs[0]
	var gross int64
	var renewalType string
	if err := db.QueryRow(ctx, `SELECT e.gross_minor,s.renewal_discount_type FROM marketplace_escrows e JOIN services s ON s.id=e.service_id WHERE s.id=$1`, serviceID).Scan(&gross, &renewalType); err != nil || gross != 2400 || renewalType != "percent" {
		t.Fatalf("escrow gross=%d renewal=%q err=%v", gross, renewalType, err)
	}
	if _, err := order(buyerID, ""); !errors.As(err, &rule) {
		t.Fatalf("purchase limit not enforced: %v", err)
	}
	list, err := coupons.ListCoupons(ctx, hostID)
	if err != nil || len(list) != 3 || list[2].Code != "HOST20" || list[2].UsedCount != 1 {
		t.Fatalf("host coupons: %+v err=%v", list, err)
	}
	if platform, _ := coupons.ListCoupons(ctx, ""); len(platform) != 1 || platform[0].Code != "PLAT5" {
		t.Fatalf("platform coupons: %+v", platform)
	}

	// The renewal keeps the 20% discount.
	if _, err := db.Exec(ctx, `UPDATE services SET status='active',node_id=$2 WHERE id=$1`, serviceID, nodeID); err != nil {
		t.Fatal(err)
	}
	if created, err := NewLifecycleStore(db).createRenewal(ctx, 40*24*time.Hour); err != nil || !created {
		t.Fatalf("renewal: %v %v", created, err)
	}
	var renewalID string
	var renewalTotal int64
	if err := db.QueryRow(ctx, `SELECT id,total_minor FROM invoices WHERE service_id=$1 AND kind='renewal'`, serviceID).Scan(&renewalID, &renewalTotal); err != nil || renewalTotal != 2400 {
		t.Fatalf("renewal total=%d err=%v", renewalTotal, err)
	}
	if _, err := billing.PayInvoiceWithBalance(ctx, buyerID, renewalID, buyerUser); err != nil {
		t.Fatal(err)
	}

	// Refunds: early full refund needs a known traffic reading under 1 GiB.
	now := time.Now()
	small := int64(100 << 20)
	big := int64(2 << 30)
	// The first period is one calendar month (28-31 days) with its first day
	// used; the renewal period is untouched.
	var firstStart, firstEnd time.Time
	if err := db.QueryRow(ctx, `SELECT period_start,period_end FROM marketplace_escrows WHERE service_id=$1 ORDER BY period_start LIMIT 1`, serviceID).Scan(&firstStart, &firstEnd); err != nil {
		t.Fatal(err)
	}
	days, _ := EscrowDays(firstStart, firstEnd, now)
	prorated := 2400*int64(days-1)/int64(days) + 2400
	for _, c := range []struct {
		traffic *int64
		full    bool
		refund  int64
	}{{nil, false, prorated}, {&big, false, prorated}, {&small, true, 4800}} {
		q, err := market.QuoteRefund(ctx, buyerID, serviceID, now, c.traffic)
		if err != nil || !q.Available || q.Full != c.full || q.RefundMinor != c.refund || q.PaidMinor != 4800 {
			t.Fatalf("refund quote traffic=%v: %+v err=%v", c.traffic, q, err)
		}
	}
	if q, _ := market.QuoteRefund(ctx, buyerID, serviceID, now.Add(2*time.Hour), &small); q.Full {
		t.Fatal("full refund after the first hour")
	}
	if _, err := market.QuoteRefund(ctx, strangerID, serviceID, now, &small); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("stranger quoted a refund: %v", err)
	}
	before, hostBefore := balanceOf(buyerID), balanceOf(hostID)
	if _, err := market.RefundService(ctx, buyerID, serviceID, buyerUser, now, &small, 100); !errors.Is(err, ErrRefundChanged) {
		t.Fatalf("refund with a stale amount: %v", err)
	}
	refunded, err := market.RefundService(ctx, buyerID, serviceID, buyerUser, now, &small, 4800)
	if err != nil || refunded.RefundMinor != 4800 || balanceOf(buyerID) != before+4800 || balanceOf(hostID) != hostBefore {
		t.Fatalf("refund: %+v buyer %d->%d host %d->%d err=%v", refunded, before, balanceOf(buyerID), hostBefore, balanceOf(hostID), err)
	}
	var status string
	var jobs, refundedEscrows int
	if err := db.QueryRow(ctx, `SELECT s.status,(SELECT count(*) FROM provisioning_jobs j WHERE j.service_id=s.id AND j.action='terminate'),(SELECT count(*) FROM marketplace_escrows e WHERE e.service_id=s.id AND e.status='refunded') FROM services s WHERE s.id=$1`, serviceID).Scan(&status, &jobs, &refundedEscrows); err != nil {
		t.Fatal(err)
	}
	if status != "terminating" || jobs != 1 || refundedEscrows != 2 {
		t.Fatalf("after refund status=%s jobs=%d escrows=%d", status, jobs, refundedEscrows)
	}
	if q, _ := market.QuoteRefund(ctx, buyerID, serviceID, now, &small); q.Available {
		t.Fatal("refunded twice")
	}
	// The limit counts live instances only, so the buyer may buy again.
	again, err := order(buyerID, "")
	if err != nil {
		t.Fatalf("order after refund: %v", err)
	}
	bought, err := billing.PayInvoiceWithBalance(ctx, buyerID, again.InvoiceID, buyerUser)
	if err != nil {
		t.Fatal(err)
	}
	tradeMarket(t, ctx, db, bought.ServiceIDs[0], hostID, buyerID, buyerUser, strangerID)
}

// tradeMarket resells the buyer's hosted instance to the stranger.
func tradeMarket(t *testing.T, ctx context.Context, db *pgxpool.Pool, serviceID, hostID, sellerID, sellerUser, buyerID string) {
	t.Helper()
	trade := NewTradeStore(db)
	rx, tx := int64(3000), int64(1000)
	snapshot := &ListingTraffic{TotalBytes: 4000, RXBytes: &rx, TXBytes: &tx}
	billing := NewBillingStore(db)
	balanceOf := func(accountID string) int64 {
		wallet, err := billing.Wallet(ctx, accountID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return wallet.BalanceMinor
	}
	var rule *TradeError
	if _, err := trade.CreateListing(ctx, sellerID, sellerUser, serviceID, 5000, "", snapshot); !errors.As(err, &rule) {
		t.Fatalf("listed a provisioning instance: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE services SET status='active',root_password_ciphertext='\x01',node_id=(SELECT node_id FROM plans WHERE id=services.plan_id) WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := trade.CreateListing(ctx, sellerID, sellerUser, serviceID, 5000, "", snapshot); !errors.As(err, &rule) {
		t.Fatalf("listed an instance held for less than 31 days: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE services SET acquired_at=now()-interval '32 days' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := trade.CreateListing(ctx, buyerID, sellerUser, serviceID, 5000, "", snapshot); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("listed someone else's instance: %v", err)
	}
	listingID, err := trade.CreateListing(ctx, sellerID, sellerUser, serviceID, 5000, "急出", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trade.CreateListing(ctx, sellerID, sellerUser, serviceID, 6000, "", snapshot); !errors.As(err, &rule) {
		t.Fatalf("listed twice: %v", err)
	}
	market, err := trade.Market(ctx, buyerID)
	if err != nil || len(market) != 1 || market[0].ServiceID != "" || market[0].SellerName != "B**" || market[0].RenewalMinor == nil {
		t.Fatalf("market: %+v err=%v", market, err)
	}
	if _, err := trade.Buy(ctx, sellerID, sellerUser, listingID, 5000); !errors.As(err, &rule) {
		t.Fatalf("seller bought own listing: %v", err)
	}
	if _, err := trade.Buy(ctx, hostID, sellerUser, listingID, 5000); !errors.As(err, &rule) {
		t.Fatalf("host bought an instance on its own node: %v", err)
	}
	if _, err := trade.Buy(ctx, buyerID, sellerUser, listingID, 4000); !errors.Is(err, ErrListingChanged) {
		t.Fatalf("bought at a stale price: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE nodes SET status='offline' WHERE id=(SELECT node_id FROM services WHERE id=$1)`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := trade.Buy(ctx, buyerID, sellerUser, listingID, 5000); !errors.As(err, &rule) {
		t.Fatalf("bought an instance on an offline node: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE nodes SET status='online' WHERE id=(SELECT node_id FROM services WHERE id=$1)`, serviceID); err != nil {
		t.Fatal(err)
	}
	sellerBefore, buyerBefore := balanceOf(sellerID), balanceOf(buyerID)
	result, err := trade.Buy(ctx, buyerID, sellerUser, listingID, 5000)
	if err != nil || result.ServiceID != serviceID {
		t.Fatalf("buy: %+v err=%v", result, err)
	}
	if balanceOf(sellerID) != sellerBefore+4000 || balanceOf(buyerID) != buyerBefore-5000 {
		t.Fatalf("trade balances seller %d->%d buyer %d->%d", sellerBefore, balanceOf(sellerID), buyerBefore, balanceOf(buyerID))
	}
	var owner, escrowBuyer string
	var password []byte
	if err := db.QueryRow(ctx, `SELECT s.account_id,s.root_password_ciphertext,e.buyer_account_id FROM services s JOIN marketplace_escrows e ON e.service_id=s.id WHERE s.id=$1 AND e.status='holding'`, serviceID).Scan(&owner, &password, &escrowBuyer); err != nil {
		t.Fatal(err)
	}
	if owner != buyerID || escrowBuyer != buyerID || password != nil {
		t.Fatalf("after trade owner=%s escrow=%s password=%v", owner, escrowBuyer, password)
	}
	if _, err := trade.Buy(ctx, buyerID, sellerUser, listingID, 5000); !errors.As(err, &rule) {
		t.Fatalf("sold twice: %v", err)
	}
	// The new owner starts a fresh holding period.
	if _, err := trade.CreateListing(ctx, buyerID, sellerUser, serviceID, 5000, "", snapshot); !errors.As(err, &rule) {
		t.Fatalf("relisted right after buying: %v", err)
	}
	if mine, _ := trade.SellerListings(ctx, sellerID); len(mine) != 1 || mine[0].Status != "sold" || mine[0].Available {
		t.Fatalf("seller listings: %+v", mine)
	}

	// A listing runs until the instance expires; then it closes and the
	// instance is suspended for the renewal window before it is recycled.
	if _, err := db.Exec(ctx, `UPDATE services SET acquired_at=now()-interval '32 days',desired_runtime_status=NULL WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	// The jobs queued by the earlier listing and sale ran long ago.
	if _, err := db.Exec(ctx, `UPDATE provisioning_jobs SET status='succeeded' WHERE service_id=$1 AND status='pending'`, serviceID); err != nil {
		t.Fatal(err)
	}
	expiring, err := trade.CreateListing(ctx, buyerID, sellerUser, serviceID, 3000, "", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	dueBefore := time.Now()
	if _, err := db.Exec(ctx, `UPDATE services SET next_due_at=now()-interval '1 minute' WHERE id=$1`, serviceID); err != nil {
		t.Fatal(err)
	}
	if n, err := NewLifecycleStore(db).expireListings(ctx); err != nil || n != 1 {
		t.Fatalf("expire listings: %d err=%v", n, err)
	}
	var listingStatus, closedBy, serviceStatus string
	var recycleAt time.Time
	if err := db.QueryRow(ctx, `SELECT l.status,l.cancelled_by,s.status,s.termination_scheduled_at FROM service_listings l JOIN services s ON s.id=l.service_id WHERE l.id=$1`, expiring).Scan(&listingStatus, &closedBy, &serviceStatus, &recycleAt); err != nil {
		t.Fatal(err)
	}
	if listingStatus != "cancelled" || closedBy != "expiry" || serviceStatus != "suspended" || recycleAt.Before(dueBefore.Add(TradeRenewalWindow-time.Minute)) || recycleAt.After(time.Now().Add(TradeRenewalWindow+time.Minute)) {
		t.Fatalf("after expiry listing=%s by=%s service=%s recycle=%v", listingStatus, closedBy, serviceStatus, recycleAt)
	}
	closed, err := NewMailStore(db).ClosedListings(ctx, time.Hour)
	if err != nil || len(closed) != 1 || closed[0].ListingID != expiring || closed[0].ClosedBy != "expiry" || closed[0].TerminationAt == nil {
		t.Fatalf("closed listings for mail: %+v err=%v", closed, err)
	}

	// Staff terminating a hosted instance refunds the unused days to the
	// current holder instead of leaving the escrow stuck.
	holderBefore := balanceOf(buyerID)
	if _, err := NewProvisioningStore(db).QueueAdminServiceAction(ctx, sellerUser, serviceID, "terminate", "", "test"); err != nil {
		t.Fatal(err)
	}
	var holding int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM marketplace_escrows WHERE service_id=$1 AND status='holding'`, serviceID).Scan(&holding); err != nil || holding != 0 {
		t.Fatalf("escrow still holding after admin termination: %d err=%v", holding, err)
	}
	if balanceOf(buyerID) <= holderBefore {
		t.Fatalf("holder not refunded on termination: %d -> %d", holderBefore, balanceOf(buyerID))
	}

	// A gateway payment for an invoice that can no longer be paid goes to
	// the balance instead of failing forever.
	var paidNumber string
	var paidAmount int64
	if err := db.QueryRow(ctx, `SELECT number,total_minor FROM invoices WHERE account_id=$1 AND status='paid' AND kind='initial' ORDER BY created_at LIMIT 1`, sellerID).Scan(&paidNumber, &paidAmount); err != nil {
		t.Fatal(err)
	}
	before := balanceOf(sellerID)
	late, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "generic", ProviderEventID: "late-1", EventType: "payment.succeeded", ProviderTransactionID: "late-1", InvoiceNumber: paidNumber, AmountMinor: paidAmount, Currency: "CNY", Payload: []byte(`{}`)}, "payment_provider", "generic")
	if err != nil || !late.CreditedToBalance || balanceOf(sellerID) != before+paidAmount {
		t.Fatalf("late payment: %+v balance %d->%d err=%v", late, before, balanceOf(sellerID), err)
	}
	if _, err := billing.ProcessPayment(ctx, PaymentEvent{Provider: "manual", ProviderEventID: "late-2", EventType: "payment.succeeded", ProviderTransactionID: "late-2", InvoiceNumber: paidNumber, AmountMinor: paidAmount, Currency: "CNY", Payload: []byte(`{}`)}, "staff", ""); !errors.Is(err, ErrPaymentMismatch) {
		t.Fatalf("manual confirmation of a paid invoice: %v", err)
	}

	// Unpaid new orders expire an hour after they fall due.
	if _, err := db.Exec(ctx, `UPDATE invoices SET due_at=now()-interval '2 hours' WHERE status='open' AND kind='initial'`); err != nil {
		t.Fatal(err)
	}
	expired, err := NewLifecycleStore(db).expireUnpaidOrders(ctx)
	if err != nil || expired == 0 {
		t.Fatalf("expire unpaid orders: %d err=%v", expired, err)
	}
	var open int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE status='open' AND kind='initial'`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("open initial invoices after expiry: %d err=%v", open, err)
	}
}
