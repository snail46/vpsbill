package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// Early full refunds: a buyer of a plan with the option on gets everything
// back within an hour of purchase if the instance used at most 1 GiB.
const (
	EarlyRefundWindow  = time.Hour
	EarlyRefundTraffic = int64(1) << 30
)

var (
	ErrRefundUnavailable = errors.New("refund unavailable")
	ErrRefundChanged     = errors.New("refund amount changed")
)

// RefundTarget is what the caller needs to measure a service's traffic
// before asking for a quote.
type RefundTarget struct {
	ServiceID    string
	InstanceName string
	NodeID       string
	// StoredTrafficBytes is the last scanner measurement, if any was taken
	// after the service was created.
	StoredTrafficBytes *int64
}

func (m *MarketplaceStore) RefundTarget(ctx context.Context, accountID, serviceID string) (RefundTarget, error) {
	var target RefundTarget
	var nodeID *string
	err := m.db.QueryRow(ctx, `
		SELECT s.id,s.instance_name,s.node_id::text,CASE WHEN s.traffic_measured_at>=s.created_at THEN s.traffic_used_bytes END
		FROM services s JOIN plans p ON p.id=s.plan_id
		WHERE s.id=$1 AND s.account_id=$2 AND (p.owner_account_id IS NOT NULL OR s.status='error')
	`, serviceID, accountID).Scan(&target.ServiceID, &target.InstanceName, &nodeID, &target.StoredTrafficBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundTarget{}, ErrServiceNotFound
	}
	if nodeID != nil {
		target.NodeID = *nodeID
	}
	return target, err
}

// RefundQuote says what a buyer would get back for cancelling now.
type RefundQuote struct {
	ServiceID    string `json:"service_id"`
	InstanceName string `json:"instance_name"`
	Available    bool   `json:"available"`
	// Message explains why no refund is possible, or why it is not full.
	Message     string `json:"message,omitempty"`
	Full        bool   `json:"full"`
	EarlyRefund bool   `json:"early_refund"`
	PaidMinor   int64  `json:"paid_minor"`
	RefundMinor int64  `json:"refund_minor"`
	Currency    string `json:"currency"`
	// TrafficBytes is nil when the traffic could not be measured.
	TrafficBytes *int64    `json:"traffic_bytes"`
	PurchasedAt  time.Time `json:"purchased_at"`
}

type refundEscrow struct {
	id                                        string
	gross, share, releasedGross, releasedHost int64
	start, end                                time.Time
	refund, hostTarget                        int64
}

// settleRefund works out one escrow's refund. A full refund returns all that
// is still held; otherwise the buyer gets the whole days left (the current
// day counts as used) and the host keeps its share of the used part.
func (e *refundEscrow) settle(now time.Time, full bool) {
	remaining := e.gross - e.releasedGross
	if full {
		e.refund = remaining
	} else {
		total, _ := EscrowDays(e.start, e.end, now)
		used := 0
		if now.After(e.start) {
			used = int(math.Ceil(now.Sub(e.start).Hours() / 24))
		}
		used = min(used, total)
		e.refund = e.gross * int64(total-used) / int64(total)
		if e.refund > remaining {
			e.refund = remaining
		}
	}
	e.hostTarget = e.releasedHost
	if e.gross > 0 {
		e.hostTarget = max(e.share*(e.gross-e.refund)/e.gross, e.releasedHost)
	}
}

type refundPlan struct {
	RefundQuote
	accountID, status, planName string
	hostID                      *string
	nodeID                      *string
	// platformPaid is what was paid for a platform service that failed to
	// provision; platform services have no escrow.
	platformPaid int64
	escrows      []refundEscrow
}

func (m *MarketplaceStore) planRefund(ctx context.Context, tx pgx.Tx, accountID, serviceID string, now time.Time, traffic *int64) (refundPlan, error) {
	var plan refundPlan
	err := tx.QueryRow(ctx, `
		SELECT s.id,s.instance_name,s.account_id,s.status,s.node_id,s.created_at,p.early_refund,p.owner_account_id,p.name,a.default_currency
		FROM services s JOIN plans p ON p.id=s.plan_id JOIN accounts a ON a.id=s.account_id
		WHERE s.id=$1 AND s.account_id=$2 FOR UPDATE OF s
	`, serviceID, accountID).Scan(&plan.ServiceID, &plan.InstanceName, &plan.accountID, &plan.status, &plan.nodeID, &plan.PurchasedAt,
		&plan.EarlyRefund, &plan.hostID, &plan.planName, &plan.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return plan, ErrServiceNotFound
	}
	if err != nil {
		return plan, err
	}
	plan.TrafficBytes = traffic
	if plan.hostID == nil {
		// Platform services are refunded only when provisioning failed for
		// good: the whole unit price paid comes back.
		if plan.status != "error" || plan.nodeID != nil {
			return plan, ErrServiceNotFound
		}
		err := tx.QueryRow(ctx, `
			SELECT coalesce(sum(l.total_minor),0)::bigint / greatest(max(oi.quantity),1)
			FROM services s JOIN order_items oi ON oi.id=s.order_item_id
			JOIN invoices i ON i.order_id=oi.order_id AND i.status='paid'
			JOIN invoice_lines l ON l.invoice_id=i.id AND l.order_item_id=oi.id
			WHERE s.id=$1
		`, serviceID).Scan(&plan.platformPaid)
		if err != nil {
			return plan, err
		}
		plan.Full, plan.Available = true, true
		plan.Message = "实例开通失败，全额退款"
		plan.PaidMinor, plan.RefundMinor = plan.platformPaid, plan.platformPaid
		return plan, nil
	}
	switch plan.status {
	case "active", "overdue", "suspended":
	case "error":
		if plan.nodeID != nil {
			plan.Message = "实例处于异常状态，请提交工单处理"
			return plan, nil
		}
	case "provisioning":
		plan.Message = "实例正在开通，开通完成后才能申请退款"
		return plan, nil
	default:
		plan.Message = "该实例已终止"
		return plan, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id,gross_minor,host_share_minor,released_gross_minor,released_host_minor,period_start,period_end
		FROM marketplace_escrows WHERE service_id=$1 AND status='holding' ORDER BY period_start FOR UPDATE
	`, serviceID)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var e refundEscrow
		if err := rows.Scan(&e.id, &e.gross, &e.share, &e.releasedGross, &e.releasedHost, &e.start, &e.end); err != nil {
			rows.Close()
			return plan, err
		}
		plan.escrows = append(plan.escrows, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return plan, err
	}
	switch {
	case plan.status == "error":
		// Provisioning failed for good: nothing was delivered.
		plan.Full = true
		plan.Message = "实例开通失败，全额退款"
	case !plan.EarlyRefund:
		plan.Message = "该套餐未开启早期全额退款，按剩余天数比例退款"
	case now.Sub(plan.PurchasedAt) > EarlyRefundWindow:
		plan.Message = "已超过购买后 1 小时，按剩余天数比例退款"
	case traffic == nil:
		plan.Message = "暂时无法读取实例流量，按剩余天数比例退款"
	case *traffic > EarlyRefundTraffic:
		plan.Message = "实例流量已超过 1GB，按剩余天数比例退款"
	default:
		plan.Full = true
	}
	for i := range plan.escrows {
		plan.escrows[i].settle(now, plan.Full)
		plan.PaidMinor += plan.escrows[i].gross
		plan.RefundMinor += plan.escrows[i].refund
	}
	plan.Available = true
	return plan, nil
}

func (m *MarketplaceStore) QuoteRefund(ctx context.Context, accountID, serviceID string, now time.Time, traffic *int64) (RefundQuote, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return RefundQuote{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	plan, err := m.planRefund(ctx, tx, accountID, serviceID, now, traffic)
	return plan.RefundQuote, err
}

// RefundResult is a completed refund, with contacts for the notices.
type RefundResult struct {
	RefundQuote
	PlanName   string `json:"-"`
	BuyerName  string `json:"-"`
	BuyerEmail string `json:"-"`
	HostName   string `json:"-"`
	HostEmail  string `json:"-"`
	HostMinor  int64  `json:"-"`
}

// RefundService cancels a hosted service at the buyer's request: the refund
// goes to the buyer's balance, the host is paid for the time used and the
// instance is deleted from the node. It fails with ErrRefundChanged unless
// the refund still equals expectedMinor, the amount the buyer confirmed.
func (m *MarketplaceStore) RefundService(ctx context.Context, accountID, serviceID, userID string, now time.Time, traffic *int64, expectedMinor int64) (RefundResult, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return RefundResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	plan, err := m.planRefund(ctx, tx, accountID, serviceID, now, traffic)
	if err != nil {
		return RefundResult{}, err
	}
	if !plan.Available {
		return RefundResult{RefundQuote: plan.RefundQuote}, ErrRefundUnavailable
	}
	if plan.RefundMinor != expectedMinor {
		return RefundResult{RefundQuote: plan.RefundQuote}, ErrRefundChanged
	}
	result := RefundResult{RefundQuote: plan.RefundQuote, PlanName: plan.planName}
	for _, e := range plan.escrows {
		if delta := e.hostTarget - e.releasedHost; delta > 0 {
			if _, err := applyWalletChange(ctx, tx, walletChange{
				AccountID: *plan.hostID, Kind: "earning", AmountMinor: delta,
				Description:   fmt.Sprintf("托管收益：%s 已使用部分（买家退款）", plan.InstanceName),
				ReferenceType: "escrow", ReferenceID: e.id, DedupKey: "escrow:" + e.id + ":refund-settle",
			}); err != nil {
				return RefundResult{}, err
			}
			result.HostMinor += delta
		}
		if _, err := tx.Exec(ctx, `UPDATE marketplace_escrows SET status='refunded',refunded_minor=$2,released_gross_minor=gross_minor-$2,released_host_minor=$3,cleared_at=now(),updated_at=now() WHERE id=$1`,
			e.id, e.refund, e.hostTarget); err != nil {
			return RefundResult{}, err
		}
	}
	if plan.RefundMinor > 0 {
		label := "按剩余天数比例退款"
		switch {
		case plan.status == "error":
			label = "开通失败全额退款"
		case plan.Full:
			label = "早期全额退款"
		}
		if _, err := applyWalletChange(ctx, tx, walletChange{
			AccountID: accountID, Kind: "refund", AmountMinor: plan.RefundMinor,
			Description:   fmt.Sprintf("实例 %s 退款（%s）", plan.InstanceName, label),
			ReferenceType: "service", ReferenceID: serviceID, DedupKey: "refund:" + serviceID,
		}); err != nil {
			return RefundResult{}, err
		}
	}
	reason := "买家申请退款"
	if _, err := tx.Exec(ctx, `UPDATE provisioning_jobs SET status='dead',locked_at=NULL,locked_by=NULL,last_error='cancelled by refund',updated_at=now() WHERE service_id=$1 AND status IN ('pending','failed')`, serviceID); err != nil {
		return RefundResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invoices SET status='void',updated_at=now() WHERE service_id=$1 AND status='open'`, serviceID); err != nil {
		return RefundResult{}, err
	}
	if plan.nodeID == nil {
		if _, err := tx.Exec(ctx, `UPDATE services SET status='terminated',termination_reason=$2,refunded_at=now(),runtime_status='stopped',desired_runtime_status=NULL,terminated_at=now(),updated_at=now() WHERE id=$1`, serviceID, reason); err != nil {
			return RefundResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE inventory_reservations SET status='released',released_at=now() WHERE service_id=$1 AND status='reserved'`, serviceID); err != nil {
			return RefundResult{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE services SET status='terminating',termination_reason=$2,refunded_at=now(),desired_runtime_status=NULL,updated_at=now() WHERE id=$1`, serviceID, reason); err != nil {
			return RefundResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'terminate',$2,jsonb_build_object('source','refund')) ON CONFLICT(deduplication_key) DO NOTHING`, serviceID, serviceID+":refund-terminate"); err != nil {
			return RefundResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.refunded',$2,jsonb_build_object('refund_minor',$3::bigint,'full',$4::boolean)) ON CONFLICT(deduplication_key) DO NOTHING`,
		serviceID, serviceID+":refunded:v1", plan.RefundMinor, plan.Full); err != nil {
		return RefundResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',nullif($1,'')::uuid,'service.refunded','service',$2,jsonb_build_object('refund_minor',$3::bigint,'full',$4::boolean,'traffic_bytes',$5::bigint,'host_minor',$6::bigint))`,
		userID, serviceID, plan.RefundMinor, plan.Full, traffic, result.HostMinor); err != nil {
		return RefundResult{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT b.display_name,b.billing_email,coalesce(h.display_name,''),coalesce(h.billing_email,'') FROM accounts b LEFT JOIN accounts h ON h.id=$2 WHERE b.id=$1`, accountID, plan.hostID).
		Scan(&result.BuyerName, &result.BuyerEmail, &result.HostName, &result.HostEmail); err != nil {
		return RefundResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundResult{}, err
	}
	return result, nil
}

// settleTerminatedEscrow closes the escrow of a hosted service that staff
// terminated: the holder gets the unused whole days back as balance and the
// host is paid for the days used. Platform services have no escrow.
func settleTerminatedEscrow(ctx context.Context, tx pgx.Tx, serviceID string, now time.Time) error {
	var holder, instance string
	if err := tx.QueryRow(ctx, `SELECT account_id,instance_name FROM services WHERE id=$1`, serviceID).Scan(&holder, &instance); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT id,host_account_id,gross_minor,host_share_minor,released_gross_minor,released_host_minor,period_start,period_end
		FROM marketplace_escrows WHERE service_id=$1 AND status='holding' ORDER BY period_start FOR UPDATE
	`, serviceID)
	if err != nil {
		return err
	}
	type held struct {
		refundEscrow
		host string
	}
	escrows := make([]held, 0)
	for rows.Next() {
		var e held
		if err := rows.Scan(&e.id, &e.host, &e.gross, &e.share, &e.releasedGross, &e.releasedHost, &e.start, &e.end); err != nil {
			rows.Close()
			return err
		}
		escrows = append(escrows, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var refund int64
	for _, e := range escrows {
		e.settle(now, false)
		if _, err := applyWalletChange(ctx, tx, walletChange{
			AccountID: e.host, Kind: "earning", AmountMinor: e.hostTarget - e.releasedHost,
			Description:   fmt.Sprintf("托管收益：%s 已使用部分（管理员终止实例）", instance),
			ReferenceType: "escrow", ReferenceID: e.id, DedupKey: "escrow:" + e.id + ":terminate-settle",
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE marketplace_escrows SET status='refunded',refunded_minor=$2,released_gross_minor=gross_minor-$2,released_host_minor=$3,cleared_at=now(),updated_at=now() WHERE id=$1`,
			e.id, e.refund, e.hostTarget); err != nil {
			return err
		}
		refund += e.refund
	}
	_, err = applyWalletChange(ctx, tx, walletChange{
		AccountID: holder, Kind: "refund", AmountMinor: refund,
		Description:   fmt.Sprintf("实例 %s 被管理员终止，按剩余天数退款", instance),
		ReferenceType: "service", ReferenceID: serviceID, DedupKey: "terminate-refund:" + serviceID,
	})
	return err
}
