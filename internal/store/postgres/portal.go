package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"vpsbill/internal/clock"
	"vpsbill/internal/security"
)

var (
	ErrServiceNotFound          = errors.New("service not found")
	ErrServiceActionUnavailable = errors.New("service action unavailable")
	ErrServiceListed            = errors.New("service is listed in the trading market")
	ErrTrafficLocked            = errors.New("service used up its monthly traffic")
	ErrActionInProgress         = errors.New("same service action is already in progress")
)

type PortalStore struct{ db *pgxpool.Pool }

func NewPortalStore(db *pgxpool.Pool) *PortalStore { return &PortalStore{db: db} }

type CustomerService struct {
	ID                   string `json:"id"`
	PlanName             string `json:"plan_name"`
	RegionName           string `json:"region_name"`
	Status               string `json:"status"`
	RuntimeStatus        string `json:"runtime_status"`
	DesiredRuntimeStatus string `json:"desired_runtime_status,omitempty"`
	InstanceName         string `json:"instance_name"`
	Virtualization       string `json:"virtualization"`
	VCPU                 int    `json:"vcpu"`
	RAMMB                int    `json:"ram_mb"`
	DiskGB               int    `json:"disk_gb"`
	TrafficGB            int    `json:"traffic_gb"`
	// NetworkDownMbps is the plan's download bandwidth, shown as the
	// instance's bandwidth (0 = unlimited).
	NetworkDownMbps    int        `json:"network_down_mbps"`
	PrimaryIPv4        string     `json:"primary_ipv4,omitempty"`
	PrimaryIPv6        string     `json:"primary_ipv6,omitempty"`
	NextDueAt          *time.Time `json:"next_due_at,omitempty"`
	GraceUntil         *time.Time `json:"grace_until,omitempty"`
	TerminationAt      *time.Time `json:"termination_scheduled_at,omitempty"`
	LastReconciledAt   *time.Time `json:"last_reconciled_at,omitempty"`
	LastReconcileError string     `json:"last_reconcile_error,omitempty"`
	// HostName is set for instances on a hosted node; TerminationReason
	// explains a clearance.
	HostName          string `json:"host_name,omitempty"`
	TerminationReason string `json:"termination_reason,omitempty"`
	// AcquiredAt starts the trading hold; ListingID and ListingPriceMinor are
	// set while the instance is listed in the trading market.
	AcquiredAt        time.Time `json:"acquired_at"`
	ListingID         string    `json:"listing_id,omitempty"`
	ListingPriceMinor int64     `json:"listing_price_minor,omitempty"`
	// This month's two-way traffic as last measured, and the month the
	// instance was stopped for using it up.
	TrafficUsedBytes   *int64 `json:"traffic_used_bytes"`
	TrafficRXBytes     *int64 `json:"traffic_rx_bytes"`
	TrafficTXBytes     *int64 `json:"traffic_tx_bytes"`
	TrafficLockedMonth string `json:"traffic_locked_month,omitempty"`
	// Source is "platform" or "hosted"; ViaTrade marks an instance the
	// customer bought in the trading market. NodeID names a hosted node,
	// whose chat room buyers can join.
	Source   string `json:"source"`
	ViaTrade bool   `json:"via_trade"`
	NodeID   string `json:"node_id,omitempty"`
	// TemplateID is the installed system.
	TemplateID   string `json:"template_id"`
	AutoRenew    bool   `json:"auto_renew"`
	BillingCycle string `json:"billing_cycle"`
	Currency     string `json:"currency"`
	// RenewalPriceMinor is what the next renewal costs, with a hosted price
	// lock and a recurring coupon applied; nil when the plan has no price.
	RenewalPriceMinor *int64 `json:"renewal_price_minor"`
}

type CustomerServiceAccess struct {
	ServiceID      string
	Status         string
	RuntimeStatus  string
	InstanceName   string
	Virtualization string
	NodeEndpoint
	RootPasswordCiphertext []byte
	AllowedTemplateIDs     []string
	PortMappingCount       int
	// Listed is set while the instance is for sale in the trading market;
	// the seller can then only look at it.
	Listed bool
}

func (s *PortalStore) ServiceAccess(ctx context.Context, accountID, serviceID string) (CustomerServiceAccess, error) {
	var result CustomerServiceAccess
	err := s.db.QueryRow(ctx, `
		SELECT s.id,s.status,s.runtime_status,s.instance_name,p.virtualization,n.provider_type,n.base_url,
		       n.api_key_ciphertext,n.provider_options,s.root_password_ciphertext,p.allowed_template_ids,p.port_mapping_count,
		       EXISTS(SELECT 1 FROM service_listings l WHERE l.service_id=s.id AND l.status='listed')
		FROM services s
		JOIN plans p ON p.id=s.plan_id
		JOIN nodes n ON n.id=s.node_id
		WHERE s.id=$1 AND s.account_id=$2
	`, serviceID, accountID).Scan(&result.ServiceID, &result.Status, &result.RuntimeStatus, &result.InstanceName,
		&result.Virtualization, &result.ProviderType, &result.BaseURL, &result.APIKeyCiphertext, &result.ProviderOptions,
		&result.RootPasswordCiphertext, &result.AllowedTemplateIDs, &result.PortMappingCount, &result.Listed)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerServiceAccess{}, ErrServiceNotFound
	}
	return result, err
}

func (s *PortalStore) UpdateRootPassword(ctx context.Context, accountID, serviceID, userID string, ciphertext []byte, ip, userAgent string) error {
	if err := s.SaveRootPassword(ctx, accountID, serviceID, ciphertext); err != nil {
		return err
	}
	return s.RecordServiceOperation(ctx, userID, serviceID, "service.password_reset", ip, userAgent, map[string]any{})
}

func (s *PortalStore) SaveRootPassword(ctx context.Context, accountID, serviceID string, ciphertext []byte) error {
	command, err := s.db.Exec(ctx, `UPDATE services SET root_password_ciphertext=$3,updated_at=now() WHERE id=$1 AND account_id=$2`, serviceID, accountID, ciphertext)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrServiceNotFound
	}
	return nil
}

func (s *PortalStore) RecordServiceOperation(ctx context.Context, userID, serviceID, action, ip, userAgent string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var ipValue any
	if strings.TrimSpace(ip) != "" {
		ipValue = strings.TrimSpace(ip)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,ip,user_agent,metadata)
		VALUES('customer',$1,$2,'service',$3,$4,$5,$6::jsonb)
	`, userID, action, serviceID, ipValue, userAgent, body)
	return err
}

type CustomerInvoice struct {
	ID           string     `json:"id"`
	Number       string     `json:"number"`
	Status       string     `json:"status"`
	Kind         string     `json:"kind"`
	ServiceID    *string    `json:"service_id,omitempty"`
	Currency     string     `json:"currency"`
	TotalMinor   int64      `json:"total_minor"`
	BalanceMinor int64      `json:"balance_minor"`
	DueAt        time.Time  `json:"due_at"`
	PaidAt       *time.Time `json:"paid_at,omitempty"`
	PeriodStart  *time.Time `json:"period_start,omitempty"`
	PeriodEnd    *time.Time `json:"period_end,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}
type CustomerTransaction struct {
	ID                    string    `json:"id"`
	InvoiceNumber         string    `json:"invoice_number,omitempty"`
	Provider              string    `json:"provider"`
	ProviderTransactionID string    `json:"provider_transaction_id,omitempty"`
	Type                  string    `json:"type"`
	Status                string    `json:"status"`
	Currency              string    `json:"currency"`
	AmountMinor           int64     `json:"amount_minor"`
	CreatedAt             time.Time `json:"created_at"`
}

func (s *PortalStore) ListServices(ctx context.Context, accountID string) ([]CustomerService, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id,p.name,r.name,s.status,s.runtime_status,coalesce(s.desired_runtime_status,''),s.instance_name,
		       p.virtualization,p.vcpu,p.ram_mb,p.disk_gb,p.traffic_gb,p.network_down_mbps,coalesce(host(s.primary_ipv4),''),coalesce(host(s.primary_ipv6),''),
		       s.next_due_at,s.grace_until,s.termination_scheduled_at,s.last_reconciled_at,coalesce(s.last_reconcile_error,''),
		       coalesce(h.display_name,''),coalesce(s.termination_reason,''),
		       s.acquired_at,coalesce(l.id::text,''),coalesce(l.price_minor,0),
		       s.traffic_used_bytes,s.traffic_rx_bytes,s.traffic_tx_bytes,coalesce(s.traffic_locked_month,''),
		       CASE WHEN p.owner_account_id IS NULL THEN 'platform' ELSE 'hosted' END,
		       EXISTS(SELECT 1 FROM service_listings b WHERE b.service_id=s.id AND b.status='sold' AND b.buyer_account_id=s.account_id),
		       CASE WHEN p.owner_account_id IS NULL THEN '' ELSE coalesce(p.node_id::text,'') END,
		       coalesce(s.template_id, oi.configuration->>'template_id', p.default_template_id),
		       s.auto_renew, s.billing_cycle, a.default_currency,
		       coalesce(pp.amount_minor, s.list_price_minor), s.renewal_price_minor, s.renewal_discount_type, s.renewal_discount_value
		FROM services s JOIN plans p ON p.id=s.plan_id JOIN regions r ON r.id=s.region_id JOIN accounts a ON a.id=s.account_id
		LEFT JOIN accounts h ON h.id=p.owner_account_id
		LEFT JOIN order_items oi ON oi.id=s.order_item_id
		LEFT JOIN service_listings l ON l.service_id=s.id AND l.status='listed'
		LEFT JOIN LATERAL (SELECT amount_minor FROM plan_prices WHERE plan_id=s.plan_id AND currency=a.default_currency AND billing_cycle=s.billing_cycle
		                   AND active_from<=now() AND (active_until IS NULL OR active_until>now()) ORDER BY active_from DESC LIMIT 1) pp ON true
		WHERE s.account_id=$1 ORDER BY s.created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CustomerService, 0)
	for rows.Next() {
		var row CustomerService
		var price, locked, discountValue *int64
		var discountType *string
		if err := rows.Scan(&row.ID, &row.PlanName, &row.RegionName, &row.Status, &row.RuntimeStatus, &row.DesiredRuntimeStatus, &row.InstanceName, &row.Virtualization, &row.VCPU, &row.RAMMB, &row.DiskGB, &row.TrafficGB, &row.NetworkDownMbps, &row.PrimaryIPv4, &row.PrimaryIPv6, &row.NextDueAt, &row.GraceUntil, &row.TerminationAt, &row.LastReconciledAt, &row.LastReconcileError, &row.HostName, &row.TerminationReason, &row.AcquiredAt, &row.ListingID, &row.ListingPriceMinor,
			&row.TrafficUsedBytes, &row.TrafficRXBytes, &row.TrafficTXBytes, &row.TrafficLockedMonth,
			&row.Source, &row.ViaTrade, &row.NodeID, &row.TemplateID, &row.AutoRenew, &row.BillingCycle, &row.Currency,
			&price, &locked, &discountType, &discountValue); err != nil {
			return nil, err
		}
		if price != nil {
			amount := renewalAmount(*price, locked, discountType, discountValue)
			row.RenewalPriceMinor = &amount
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *PortalStore) ListInvoices(ctx context.Context, accountID string) ([]CustomerInvoice, error) {
	rows, err := s.db.Query(ctx, `SELECT id,number,status,kind,service_id,currency,total_minor,balance_minor,due_at,paid_at,period_start,period_end,created_at FROM invoices WHERE account_id=$1 ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CustomerInvoice, 0)
	for rows.Next() {
		var row CustomerInvoice
		if err := rows.Scan(&row.ID, &row.Number, &row.Status, &row.Kind, &row.ServiceID, &row.Currency, &row.TotalMinor, &row.BalanceMinor, &row.DueAt, &row.PaidAt, &row.PeriodStart, &row.PeriodEnd, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *PortalStore) ListTransactions(ctx context.Context, accountID string) ([]CustomerTransaction, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id,coalesce(i.number,''),t.provider,coalesce(t.provider_transaction_id,''),t.type,t.status,t.currency,t.amount_minor,t.created_at
		FROM transactions t LEFT JOIN invoices i ON i.id=t.invoice_id WHERE t.account_id=$1 ORDER BY t.created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CustomerTransaction, 0)
	for rows.Next() {
		var row CustomerTransaction
		if err := rows.Scan(&row.ID, &row.InvoiceNumber, &row.Provider, &row.ProviderTransactionID, &row.Type, &row.Status, &row.Currency, &row.AmountMinor, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *PortalStore) QueueServiceAction(ctx context.Context, accountID, userID, serviceID, action, ip, userAgent string) (string, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return "", ErrServiceActionUnavailable
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, runtimeStatus, desiredRuntimeStatus, instanceName, lockedMonth string
	var nodeID *string
	var listed bool
	err = tx.QueryRow(ctx, `SELECT status,runtime_status,coalesce(desired_runtime_status,''),instance_name,node_id,coalesce(traffic_locked_month,''),
		EXISTS(SELECT 1 FROM service_listings l WHERE l.service_id=services.id AND l.status='listed')
		FROM services WHERE id=$1 AND account_id=$2 FOR UPDATE`, serviceID, accountID).Scan(&status, &runtimeStatus, &desiredRuntimeStatus, &instanceName, &nodeID, &lockedMonth, &listed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrServiceNotFound
	}
	if err != nil {
		return "", err
	}
	if !CustomerUsable(status) || nodeID == nil || desiredRuntimeStatus != "" {
		return "", ErrServiceActionUnavailable
	}
	if listed {
		return "", ErrServiceListed
	}
	if action != "stop" && lockedMonth == clock.Month(time.Now()) {
		return "", ErrTrafficLocked
	}
	if action == "start" && runtimeStatus == "running" {
		return "", ErrServiceActionUnavailable
	}
	if (action == "stop" || action == "restart") && runtimeStatus == "stopped" {
		return "", ErrServiceActionUnavailable
	}
	token, _, err := security.NewToken()
	if err != nil {
		return "", err
	}
	if len(token) > 16 {
		token = token[:16]
	}
	dedup := fmt.Sprintf("%s:%s:%s", serviceID, action, token)
	desired := "running"
	if action == "stop" {
		desired = "stopped"
	}
	var jobID string
	err = tx.QueryRow(ctx, `
		INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload)
		VALUES($1,$2,$3,jsonb_build_object('requested_by',$4::text,'source','customer_portal'))
		ON CONFLICT DO NOTHING RETURNING id
	`, serviceID, action, dedup, userID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrActionInProgress
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "UPDATE services SET desired_runtime_status=$2,updated_at=now() WHERE id=$1", serviceID, desired); err != nil {
		return "", err
	}
	var ipValue any
	if strings.TrimSpace(ip) != "" {
		ipValue = strings.TrimSpace(ip)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,ip,user_agent,metadata)
		VALUES('customer',$1,$2,'service',$3,$4,$5,jsonb_build_object('instance_name',$6::text,'job_id',$7::text))
	`, userID, "service."+action, serviceID, ipValue, userAgent, instanceName, jobID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return jobID, nil
}

// ObserveRuntime records a live runtime status read by the customer portal,
// so a finished power action stops showing as in progress without waiting
// for the next reconcile pass.
func (s *PortalStore) ObserveRuntime(ctx context.Context, serviceID, runtimeStatus string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE services SET runtime_status=$2,
		    desired_runtime_status=CASE WHEN desired_runtime_status=$2 THEN NULL ELSE desired_runtime_status END,
		    last_reconcile_error=NULL, updated_at=now()
		WHERE id=$1 AND status IN ('active','overdue','suspended')
		  AND (runtime_status IS DISTINCT FROM $2 OR desired_runtime_status=$2 OR last_reconcile_error IS NOT NULL)
	`, serviceID, runtimeStatus)
	return err
}

// CustomerUsable reports whether customers may operate a service: overdue
// services keep running through the grace period, so they stay manageable.
func CustomerUsable(status string) bool { return status == "active" || status == "overdue" }
