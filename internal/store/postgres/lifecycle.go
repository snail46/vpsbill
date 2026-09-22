package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LifecycleStore struct{ db *pgxpool.Pool }

func NewLifecycleStore(db *pgxpool.Pool) *LifecycleStore { return &LifecycleStore{db: db} }

type LifecycleResult struct{ RenewalInvoices, PricingReview, Overdue, Suspended, TerminationQueued int }

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
	result.PricingReview, err = s.markPricingReview(ctx, lead)
	if err != nil {
		return result, err
	}
	result.Overdue, err = s.markOverdue(ctx, grace)
	if err != nil {
		return result, err
	}
	result.Suspended, err = s.suspendOverdue(ctx, retention)
	if err != nil {
		return result, err
	}
	result.TerminationQueued, err = s.queueTerminations(ctx)
	return result, err
}

func (s *LifecycleStore) markPricingReview(ctx context.Context, lead time.Duration) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE services s SET status='review',last_reconcile_error='renewal price unavailable; manual review required',updated_at=now() WHERE s.status='active' AND s.next_due_at<=now()+make_interval(secs=>$1) AND NOT EXISTS(SELECT 1 FROM invoices i WHERE i.service_id=s.id AND i.period_start=s.next_due_at AND i.status IN ('draft','open','paid')) AND NOT EXISTS(SELECT 1 FROM plan_prices pp JOIN accounts a ON a.id=s.account_id WHERE pp.plan_id=s.plan_id AND pp.currency=a.default_currency AND pp.billing_cycle=s.billing_cycle AND pp.active_from<=now() AND (pp.active_until IS NULL OR pp.active_until>now())) RETURNING s.id`, int(lead.Seconds()))
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
	err = tx.QueryRow(ctx, `SELECT s.id,s.account_id,s.plan_id,s.billing_cycle,a.default_currency,p.name,s.next_due_at,pp.amount_minor
		FROM services s JOIN accounts a ON a.id=s.account_id JOIN plans p ON p.id=s.plan_id JOIN LATERAL (SELECT amount_minor FROM plan_prices WHERE plan_id=s.plan_id AND currency=a.default_currency AND billing_cycle=s.billing_cycle AND active_from<=now() AND (active_until IS NULL OR active_until>now()) ORDER BY active_from DESC LIMIT 1) pp ON true
		WHERE s.status='active' AND s.next_due_at IS NOT NULL AND s.next_due_at<=now()+make_interval(secs=>$1)
		AND NOT EXISTS(SELECT 1 FROM invoices i WHERE i.service_id=s.id AND i.period_start=s.next_due_at AND i.status IN ('draft','open','paid'))
		ORDER BY s.next_due_at FOR UPDATE OF s SKIP LOCKED LIMIT 1`, int(lead.Seconds())).Scan(&serviceID, &accountID, &planID, &cycle, &currency, &planName, &nextDue, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	periodEnd := addBillingCycle(nextDue, cycle)
	number := newDocumentNumber("INV")
	var invoiceID string
	err = tx.QueryRow(ctx, `INSERT INTO invoices(number,account_id,service_id,kind,status,currency,subtotal_minor,tax_minor,total_minor,balance_minor,issued_at,due_at,period_start,period_end) VALUES($1,$2,$3,'renewal','open',$4,$5,0,$5,$5,now(),$6,$6,$7) ON CONFLICT(service_id,period_end) WHERE service_id IS NOT NULL DO NOTHING RETURNING id`, number, accountID, serviceID, currency, amount, nextDue, periodEnd).Scan(&invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO invoice_lines(invoice_id,description,quantity,unit_amount_minor,tax_minor,total_minor,metadata) VALUES($1,$2,1,$3,0,$3,jsonb_build_object('service_id',$4::text,'plan_id',$5::text,'billing_cycle',$6::text,'period_start',$7::timestamptz,'period_end',$8::timestamptz))`, invoiceID, planName+" / renewal", amount, serviceID, planID, cycle, nextDue, periodEnd); err != nil {
		return false, err
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
