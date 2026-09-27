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
	if err != nil || len(list) != 2 || list[1].Code != "HOST20" || list[1].UsedCount != 1 {
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
	for _, c := range []struct {
		traffic *int64
		full    bool
		refund  int64
	}{{nil, false, 2400*29/30 + 2400}, {&big, false, 2400*29/30 + 2400}, {&small, true, 4800}} {
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
	if _, err := order(buyerID, ""); err != nil {
		t.Fatalf("order after refund: %v", err)
	}
}
