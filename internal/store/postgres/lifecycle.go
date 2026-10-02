package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"vpsbill/internal/clock"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LifecycleStore struct{ db *pgxpool.Pool }

func NewLifecycleStore(db *pgxpool.Pool) *LifecycleStore { return &LifecycleStore{db: db} }

type LifecycleResult struct {
	RenewalInvoices, AutoRenewed, PricingReview, Overdue, LeaseEnded, Suspended, TerminationQueued, ExpiredOrders, ExpiredListings int
}

func (s *LifecycleStore) Run(ctx context.Context, lead, grace, retention time.Duration) (LifecycleResult, error) {
	var result LifecycleResult
	for i := 0; i < 100; i++ {
		created, err := s.createRenewal(ctx, lead)
		if err != nil {
			return result, err
		}
		if !created {
			break
		}
		result.RenewalInvoices++
	}
	var err error
	// Paying before overdue marking keeps auto-renewed instances active.
	result.AutoRenewed, err = s.autoRenew(ctx)
	if err != nil {
		return result, err
	}
	// Listed instances that reached their due date leave the market before
	// the overdue rules would keep them running through the grace period.
	result.ExpiredListings, err = s.expireListings(ctx)
	if err != nil {
		return result, err
	}
	result.PricingReview, err = s.markPricingReview(ctx, lead)
	if err != nil {
		return result, err
	}
	result.Overdue, err = s.markOverdue(ctx, grace)
	if err != nil {
		return result, err
	}
	result.LeaseEnded, err = s.endHostedLeases(ctx, retention)
	if err != nil {
		return result, err
	}
	result.Suspended, err = s.suspendOverdue(ctx, retention)
	if err != nil {
		return result, err
	}
	result.TerminationQueued, err = s.queueTerminations(ctx)
	if err != nil {
		return result, err
	}
	result.ExpiredOrders, err = s.expireUnpaidOrders(ctx)
	return result, err
}

// lockedRenewalPrice applies a hosted instance's price lock: it renews at
// the price it was bought at when the host has raised it since; a lower
// current price applies as it is.
func lockedRenewalPrice(price int64, locked *int64) int64 {
	if locked != nil && *locked > 0 && *locked < price {
		return *locked
	}
	return price
}

// renewalAmount is what a renewal at the current plan price costs after
// the price lock and a recurring coupon discount.
func renewalAmount(price int64, locked *int64, discountType *string, discountValue *int64) int64 {
	amount := lockedRenewalPrice(price, locked)
	if discountType != nil && discountValue != nil {
		amount -= CouponDiscount(*discountType, *discountValue, amount)
	}
	return amount
}

// autoRenewWindow is how long before expiry a renewal is paid from the
// balance, leaving customers time to switch auto-renewal off.
const autoRenewWindow = 24 * time.Hour

// autoRenew pays open renewal invoices from the balance for instances with
// auto-renewal on, once they are due within autoRenewWindow (or already
// overdue) and the balance covers them.
func (s *LifecycleStore) autoRenew(ctx context.Context) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.account_id FROM invoices i
		JOIN services s ON s.id=i.service_id JOIN accounts a ON a.id=i.account_id
		WHERE i.kind='renewal' AND i.status='open' AND i.balance_minor>0 AND s.auto_renew
		  AND s.status IN ('active','overdue','suspended') AND i.due_at<=now()+make_interval(secs=>$1)
		  AND a.balance_minor>=i.balance_minor
		ORDER BY i.due_at LIMIT 200`, autoRenewWindow.Seconds())
	if err != nil {
		return 0, err
	}
	type due struct{ invoice, account string }
	var items []due
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.invoice, &item.account); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	billing, paid := NewBillingStore(s.db), 0
	for _, item := range items {
		_, err := billing.payInvoiceWithBalance(ctx, item.account, item.invoice, "system", "")
		switch {
		case errors.Is(err, ErrInsufficientBalance), errors.Is(err, ErrInvoiceUnavailable):
		case err != nil:
			return paid, err
		default:
			paid++
		}
	}
	return paid, nil
}

// TradeRenewalWindow is how long an instance whose listing ran out stays
// suspended for its owner to renew before it is recycled.
const TradeRenewalWindow = 3 * 24 * time.Hour

// expireListings closes trading market listings whose instance reached its
// due date while listed. The instance goes back to its owner suspended
// (stopped, not usable) for TradeRenewalWindow: paying the renewal invoice
// in that time restores it, otherwise the lifecycle deletes it.
func (s *LifecycleStore) expireListings(ctx context.Context) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT l.id,s.id,s.node_id,s.runtime_status,s.next_due_at FROM service_listings l JOIN services s ON s.id=l.service_id
		WHERE l.status='listed' AND s.next_due_at<=now() AND s.status IN ('active','overdue')
		ORDER BY s.next_due_at FOR UPDATE OF l, s SKIP LOCKED LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type row struct {
		listing, service string
		nodeID           *string
		runtime          string
		due              time.Time
	}
	items := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.listing, &item.service, &item.nodeID, &item.runtime, &item.due); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range items {
		scheduled := time.Now().UTC().Add(TradeRenewalWindow)
		running := item.nodeID != nil && item.runtime != "stopped"
		if _, err = tx.Exec(ctx, `UPDATE service_listings SET status='cancelled',cancel_reason='实例已到期，已自动下架',cancelled_by='expiry',updated_at=now() WHERE id=$1`, item.listing); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE services SET status='suspended',suspended_at=now(),grace_until=NULL,termination_scheduled_at=$2,desired_runtime_status=CASE WHEN $3::boolean THEN 'stopped' ELSE NULL END,updated_at=now() WHERE id=$1`, item.service, scheduled, running); err != nil {
			return 0, err
		}
		// A stop queued by the listing itself may still be pending.
		if running {
			if _, err = tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'stop',$2,jsonb_build_object('source','billing_lifecycle')) ON CONFLICT DO NOTHING`, item.service, item.service+":listing-expired-stop:"+item.listing); err != nil {
				return 0, err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'trade.listing_expired',$2,jsonb_build_object('listing_id',$3::text,'termination_scheduled_at',$4::timestamptz)) ON CONFLICT(deduplication_key) DO NOTHING`, item.service, "trade:"+item.listing+":expired", item.listing, scheduled); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}

// expireUnpaidOrders voids new-order invoices an hour past their due date
// with no checkout still open, so held coupon uses and purchase-limit slots
// are released. A gateway payment that still arrives is credited to the
// balance.
func (s *LifecycleStore) expireUnpaidOrders(ctx context.Context) (int, error) {
	command, err := s.db.Exec(ctx, `
		WITH expired AS (
			UPDATE invoices i SET status='void',updated_at=now()
			WHERE i.kind='initial' AND i.status='open' AND i.due_at < now() - interval '1 hour'
			  AND NOT EXISTS(SELECT 1 FROM payment_intents p WHERE p.invoice_id=i.id AND p.status IN ('pending','redirected') AND p.expires_at > now())
			RETURNING i.order_id
		)
		UPDATE orders o SET status='cancelled',updated_at=now() FROM expired e WHERE o.id=e.order_id AND o.status='pending_payment'
	`)
	if err != nil {
		return 0, err
	}
	return int(command.RowsAffected()), nil
}

func (s *LifecycleStore) markPricingReview(ctx context.Context, lead time.Duration) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE services s SET status='review',last_reconcile_error='renewal price unavailable; manual review required',updated_at=now() WHERE s.status='active' AND s.next_due_at<=now()+make_interval(secs=>$1) AND NOT EXISTS(SELECT 1 FROM invoices i WHERE i.service_id=s.id AND i.period_start=s.next_due_at AND i.status IN ('draft','open','paid')) AND s.list_price_minor IS NULL AND NOT EXISTS(SELECT 1 FROM plan_prices pp JOIN accounts a ON a.id=s.account_id WHERE pp.plan_id=s.plan_id AND pp.currency=a.default_currency AND pp.billing_cycle=s.billing_cycle AND pp.active_from<=now() AND (pp.active_until IS NULL OR pp.active_until>now())) RETURNING s.id`, int(lead.Seconds()))
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.renewal_price_missing',$2,'{}') ON CONFLICT(deduplication_key) DO NOTHING`, id, id+":renewal-price-missing:v1"); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit(ctx)
}

func (s *LifecycleStore) createRenewal(ctx context.Context, lead time.Duration) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var serviceID, accountID, planID, cycle, currency, planName string
	var nextDue time.Time
	var amount int64
	var discountType, couponCode *string
	var discountValue, lockedPrice *int64
	var nodeExpires *time.Time
	// Hosted services renew only up to the host lease; one ending less than
	// a day after the due date waits until the host extends it.
	err = tx.QueryRow(ctx, `SELECT s.id,s.account_id,s.plan_id,s.billing_cycle,a.default_currency,p.name,s.next_due_at,coalesce(pp.amount_minor, s.list_price_minor),s.renewal_discount_type,s.renewal_discount_value,c.code,s.renewal_price_minor,
		(SELECT hn.expires_at FROM nodes hn WHERE hn.id=p.node_id AND hn.owner_account_id IS NOT NULL)
		FROM services s JOIN accounts a ON a.id=s.account_id JOIN plans p ON p.id=s.plan_id LEFT JOIN coupons c ON c.id=s.coupon_id LEFT JOIN LATERAL (SELECT amount_minor FROM plan_prices WHERE plan_id=s.plan_id AND currency=a.default_currency AND billing_cycle=s.billing_cycle AND active_from<=now() AND (active_until IS NULL OR active_until>now()) ORDER BY active_from DESC LIMIT 1) pp ON true
		WHERE s.status='active' AND coalesce(pp.amount_minor, s.list_price_minor) IS NOT NULL AND s.next_due_at IS NOT NULL AND s.next_due_at<=now()+make_interval(secs=>$1)
		AND NOT EXISTS(SELECT 1 FROM invoices i WHERE i.service_id=s.id AND i.period_start=s.next_due_at AND i.status IN ('draft','open','paid'))
		AND NOT EXISTS(SELECT 1 FROM nodes hn WHERE hn.id=p.node_id AND hn.owner_account_id IS NOT NULL AND (hn.expires_at + 1)::timestamptz < s.next_due_at + interval '1 day')
		ORDER BY s.next_due_at FOR UPDATE OF s SKIP LOCKED LIMIT 1`, int(lead.Seconds())).Scan(&serviceID, &accountID, &planID, &cycle, &currency, &planName, &nextDue, &amount, &discountType, &discountValue, &couponCode, &lockedPrice, &nodeExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	amount, periodEnd := ProrateToLease(lockedRenewalPrice(amount, lockedPrice), nextDue, LeaseEnd(nodeExpires), cycle)
	description := planName + " / renewal"
	if periodEnd.Before(addBillingCycle(nextDue, cycle)) {
		description += "（按母机到期折算至 " + periodEnd.Add(-time.Second).In(clock.Zone).Format("2006-01-02") + "）"
	}
	discount := amount - renewalAmount(amount, nil, discountType, discountValue)
	number := newDocumentNumber("INV")
	var invoiceID string
	err = tx.QueryRow(ctx, `INSERT INTO invoices(number,account_id,service_id,kind,status,currency,subtotal_minor,tax_minor,total_minor,balance_minor,issued_at,due_at,period_start,period_end) VALUES($1,$2,$3,'renewal','open',$4,$5,0,$5,$5,now(),$6,$6,$7) ON CONFLICT(service_id,period_end) WHERE service_id IS NOT NULL DO NOTHING RETURNING id`, number, accountID, serviceID, currency, amount-discount, nextDue, periodEnd).Scan(&invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO invoice_lines(invoice_id,description,quantity,unit_amount_minor,tax_minor,total_minor,metadata) VALUES($1,$2,1,$3,0,$3,jsonb_build_object('service_id',$4::text,'plan_id',$5::text,'billing_cycle',$6::text,'period_start',$7::timestamptz,'period_end',$8::timestamptz))`, invoiceID, description, amount, serviceID, planID, cycle, nextDue, periodEnd); err != nil {
		return false, err
	}
	if discount > 0 {
		label := "续费同价优惠"
		if couponCode != nil {
			label = "优惠码 " + *couponCode + "（续费同价）"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO invoice_lines(invoice_id,description,quantity,unit_amount_minor,tax_minor,total_minor,metadata) VALUES($1,$2,1,$3,0,$3,jsonb_build_object('service_id',$4::text))`, invoiceID, label, -discount, serviceID); err != nil {
			return false, err
		}
		amount -= discount
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('invoice',$1,'invoice.renewal_created',$2,jsonb_build_object('invoice_number',$3::text,'service_id',$4::text,'due_at',$5::timestamptz,'amount_minor',$6::bigint,'currency',$7::text))`, invoiceID, invoiceID+":renewal-created:v1", number, serviceID, nextDue, amount, currency); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,action,target_type,target_id,metadata) VALUES('system','invoice.renewal_created','invoice',$1,jsonb_build_object('service_id',$2::text,'period_end',$3::timestamptz))`, invoiceID, serviceID, periodEnd); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *LifecycleStore) markOverdue(ctx context.Context, grace time.Duration) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE services s SET status='overdue',grace_until=s.next_due_at+make_interval(secs=>$1),updated_at=now() WHERE s.status='active' AND s.next_due_at<now() AND EXISTS(SELECT 1 FROM invoices i WHERE i.service_id=s.id AND i.kind='renewal' AND i.status='open' AND i.period_start=s.next_due_at) RETURNING s.id,s.grace_until`, int(grace.Seconds()))
	if err != nil {
		return 0, err
	}
	type row struct {
		id    string
		grace time.Time
	}
	items := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.grace); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.overdue',$2,jsonb_build_object('grace_until',$3::timestamptz)) ON CONFLICT(deduplication_key) DO NOTHING`, item.id, item.id+":overdue:"+item.grace.UTC().Format("20060102"), item.grace); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}

// endHostedLeases stops hosted services whose paid period reached the host
// lease: they cannot be renewed past it, so at the due date they are
// suspended and deleted after the retention period, like an unpaid renewal.
// A host that extends the lease before the due date lets them renew instead.
func (s *LifecycleStore) endHostedLeases(ctx context.Context, retention time.Duration) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT s.id,s.node_id,s.runtime_status,s.next_due_at FROM services s JOIN plans p ON p.id=s.plan_id JOIN nodes hn ON hn.id=p.node_id
		WHERE s.status='active' AND s.next_due_at<=now() AND hn.owner_account_id IS NOT NULL
		  AND (hn.expires_at + 1)::timestamptz < s.next_due_at + interval '1 day'
		  AND NOT EXISTS(SELECT 1 FROM invoices i WHERE i.service_id=s.id AND i.period_start=s.next_due_at AND i.status IN ('open','paid'))
		ORDER BY s.next_due_at FOR UPDATE OF s SKIP LOCKED LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id      string
		nodeID  *string
		runtime string
		due     time.Time
	}
	items := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.nodeID, &item.runtime, &item.due); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		scheduled := time.Now().UTC().Add(retention)
		running := item.nodeID != nil && item.runtime != "stopped"
		if _, err = tx.Exec(ctx, `UPDATE services SET status='suspended',suspended_at=now(),termination_scheduled_at=$2,desired_runtime_status=CASE WHEN $3::boolean THEN 'stopped' ELSE NULL END,last_reconcile_error='母机租约已到期，实例随母机到期',updated_at=now() WHERE id=$1`, item.id, scheduled, running); err != nil {
			return 0, err
		}
		if running {
			if _, err = tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'stop',$2,jsonb_build_object('source','lease_ended')) ON CONFLICT(deduplication_key) DO NOTHING`, item.id, item.id+":lease-stop:"+item.due.UTC().Format("20060102")); err != nil {
				return 0, err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.lease_ended',$2,jsonb_build_object('termination_scheduled_at',$3::timestamptz)) ON CONFLICT(deduplication_key) DO NOTHING`, item.id, item.id+":lease-ended:"+item.due.UTC().Format("20060102"), scheduled); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}

func (s *LifecycleStore) suspendOverdue(ctx context.Context, retention time.Duration) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id,node_id,runtime_status,next_due_at FROM services WHERE status='overdue' AND grace_until<=now() ORDER BY grace_until FOR UPDATE SKIP LOCKED LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id      string
		nodeID  *string
		runtime string
		due     time.Time
	}
	items := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.nodeID, &item.runtime, &item.due); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		scheduled := time.Now().UTC().Add(retention)
		if _, err = tx.Exec(ctx, `UPDATE services SET status='suspended',suspended_at=now(),termination_scheduled_at=$2,desired_runtime_status=CASE WHEN $3::boolean THEN 'stopped' ELSE NULL END,updated_at=now() WHERE id=$1`, item.id, scheduled, item.nodeID != nil && item.runtime != "stopped"); err != nil {
			return 0, err
		}
		if item.nodeID != nil && item.runtime != "stopped" {
			dedup := fmt.Sprintf("%s:overdue-stop:%s", item.id, item.due.UTC().Format("20060102"))
			if _, err = tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'stop',$2,jsonb_build_object('source','billing_lifecycle')) ON CONFLICT(deduplication_key) DO NOTHING`, item.id, dedup); err != nil {
				return 0, err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.suspended_overdue',$2,jsonb_build_object('termination_scheduled_at',$3::timestamptz)) ON CONFLICT(deduplication_key) DO NOTHING`, item.id, item.id+":suspended:"+item.due.UTC().Format("20060102"), scheduled); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}

func (s *LifecycleStore) queueTerminations(ctx context.Context) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id,next_due_at FROM services WHERE status='suspended' AND termination_scheduled_at<=now() ORDER BY termination_scheduled_at FOR UPDATE SKIP LOCKED LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id  string
		due time.Time
	}
	items := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.due); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		dedup := fmt.Sprintf("%s:terminate:%s", item.id, item.due.UTC().Format("20060102"))
		if _, err = tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'terminate',$2,jsonb_build_object('source','billing_lifecycle')) ON CONFLICT(deduplication_key) DO NOTHING`, item.id, dedup); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE services SET status='terminating',updated_at=now() WHERE id=$1`, item.id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE invoices SET status='uncollectible',updated_at=now() WHERE service_id=$1 AND status='open'`, item.id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.termination_requested',$2,'{}') ON CONFLICT(deduplication_key) DO NOTHING`, item.id, dedup+":event"); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}
