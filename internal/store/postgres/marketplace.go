package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrHostedNodeNotFound = errors.New("hosted node not found")
	ErrNodeNameTaken      = errors.New("node name is already used")
	ErrNodeRetired        = errors.New("hosted node is retired")
	ErrHostInDebt         = errors.New("host balance is negative")
	ErrNotChatMember      = errors.New("not a member of this chat room")
)

// HostedOrderError explains why a hosted plan cannot be bought right now.
type HostedOrderError struct{ Message string }

func (e *HostedOrderError) Error() string { return e.Message }

// ChatNotifyChannel carries new chat message ids between API processes.
const ChatNotifyChannel = "node_chat"

type MarketplaceStore struct{ db *pgxpool.Pool }

func NewMarketplaceStore(db *pgxpool.Pool) *MarketplaceStore { return &MarketplaceStore{db: db} }

// createEscrow holds a hosted service payment for the host. Payments for
// platform plans are ignored.
func createEscrow(ctx context.Context, tx pgx.Tx, serviceID, invoiceID string, gross int64, start, end time.Time) error {
	var owner, nodeID *string
	var buyer, currency string
	if err := tx.QueryRow(ctx, `
		SELECT p.owner_account_id, p.node_id, s.account_id, a.default_currency
		FROM services s JOIN plans p ON p.id=s.plan_id JOIN accounts a ON a.id=s.account_id WHERE s.id=$1
	`, serviceID).Scan(&owner, &nodeID, &buyer, &currency); err != nil {
		return fmt.Errorf("load service for escrow: %w", err)
	}
	if owner == nil || nodeID == nil || gross <= 0 {
		return nil
	}
	var percent float64
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT marketplace_fee_percent::float8 FROM system_settings WHERE singleton=true), 20)`).Scan(&percent); err != nil {
		return fmt.Errorf("load marketplace fee: %w", err)
	}
	fee := int64(math.Round(float64(gross) * percent / 100))
	if fee > gross {
		fee = gross
	}
	if !end.After(start) {
		end = start.Add(24 * time.Hour)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO marketplace_escrows(service_id,invoice_id,node_id,host_account_id,buyer_account_id,currency,gross_minor,fee_percent,fee_minor,host_share_minor,period_start,period_end)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(invoice_id,service_id) DO NOTHING
	`, serviceID, invoiceID, *nodeID, *owner, buyer, currency, gross, percent, fee, gross-fee, start, end)
	return err
}

// validateHostedOrder checks a hosted plan before an order is created: the
// market must be open, the node listed and online, the host lease must cover
// the paid period and the node must have room for the instances.
func validateHostedOrder(ctx context.Context, tx pgx.Tx, planID, buyerID, regionID, cycle string, quantity int, vcpu, ramMB, diskGB int) error {
	var nodeID, owner *string
	if err := tx.QueryRow(ctx, `SELECT node_id, owner_account_id FROM plans WHERE id=$1`, planID).Scan(&nodeID, &owner); err != nil {
		return err
	}
	if nodeID == nil {
		return nil
	}
	var enabled bool
	var nodeRegion, status, listing string
	var expires *time.Time
	var retired *time.Time
	var freeVCPU, freeRAM, freeDisk int64
	if err := tx.QueryRow(ctx, `
		SELECT coalesce((SELECT marketplace_enabled FROM system_settings WHERE singleton=true), true), n.region_id, n.status, n.listing_status, n.expires_at, n.retired_at,
		       n.capacity_vcpu - coalesce((SELECT sum(r.vcpu) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0),
		       n.capacity_ram_mb - coalesce((SELECT sum(r.ram_mb) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0),
		       n.capacity_disk_gb - coalesce((SELECT sum(r.disk_gb) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0)
		FROM nodes n WHERE n.id=$1 FOR UPDATE OF n
	`, *nodeID).Scan(&enabled, &nodeRegion, &status, &listing, &expires, &retired, &freeVCPU, &freeRAM, &freeDisk); err != nil {
		return err
	}
	switch {
	case !enabled:
		return &HostedOrderError{"托管中心暂未开放"}
	case owner != nil && *owner == buyerID:
		return &HostedOrderError{"不能购买自己托管的母机"}
	case retired != nil || listing != "listed":
		return &HostedOrderError{"该母机已下架"}
	case status != "online":
		return &HostedOrderError{"该母机当前离线，暂不可购买"}
	case regionID != nodeRegion:
		return &HostedOrderError{"地域与母机不一致"}
	}
	if expires != nil && expires.Before(addBillingCycle(time.Now().UTC(), cycle)) {
		return &HostedOrderError{"母机租约将在本计费周期内到期，请选择更短周期"}
	}
	// Pending orders are not reserved yet, so this is a first check; the
	// scheduler enforces capacity again when the instance is created.
	q := int64(quantity)
	if freeVCPU < int64(vcpu)*q || freeRAM < int64(ramMB)*q || freeDisk < int64(diskGB)*q {
		return &HostedOrderError{"该母机剩余资源不足"}
	}
	// The purchase limit counts the buyer's live instances of the plan and
	// units in unpaid orders that can still be paid.
	var limit, held int
	if err := tx.QueryRow(ctx, `
		SELECT p.purchase_limit,
		       (SELECT count(*) FROM services s WHERE s.plan_id=p.id AND s.account_id=$2 AND s.status NOT IN ('terminating','terminated'))::int +
		       coalesce((SELECT sum(oi.quantity) FROM order_items oi JOIN invoices i ON i.order_id=oi.order_id
		                 WHERE oi.plan_id=p.id AND i.account_id=$2 AND i.status='open' AND i.due_at>now()),0)::int
		FROM plans p WHERE p.id=$1
	`, planID, buyerID).Scan(&limit, &held); err != nil {
		return err
	}
	if limit > 0 && held+quantity > limit {
		if held > 0 {
			return &HostedOrderError{fmt.Sprintf("该套餐每人限购 %d 台，你已持有或有待支付的 %d 台", limit, held)}
		}
		return &HostedOrderError{fmt.Sprintf("该套餐每人限购 %d 台", limit)}
	}
	return nil
}

type HostedService struct {
	ID                  string     `json:"id"`
	InstanceName        string     `json:"instance_name"`
	PlanName            string     `json:"plan_name"`
	Status              string     `json:"status"`
	RuntimeStatus       string     `json:"runtime_status"`
	BuyerName           string     `json:"buyer_name"`
	NextDueAt           *time.Time `json:"next_due_at"`
	CreatedAt           time.Time  `json:"created_at"`
	RemainingValueMinor int64      `json:"remaining_value_minor"`
}

type HostedNode struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	OwnerAccountID      string          `json:"owner_account_id"`
	OwnerName           string          `json:"owner_name"`
	OwnerEmail          string          `json:"owner_email,omitempty"`
	OwnerBalanceMinor   int64           `json:"owner_balance_minor"`
	RegionID            string          `json:"region_id"`
	RegionName          string          `json:"region_name"`
	Location            string          `json:"location"`
	LineDescription     string          `json:"line_description"`
	Status              string          `json:"status"`
	ListingStatus       string          `json:"listing_status"`
	VirtualizationTypes []string        `json:"virtualization_types"`
	ExpiresAt           string          `json:"expires_at"`
	TrafficQuotaGB      int             `json:"traffic_quota_gb"`
	CapacityVCPU        int64           `json:"capacity_vcpu"`
	CapacityRAMMB       int64           `json:"capacity_ram_mb"`
	CapacityDiskGB      int64           `json:"capacity_disk_gb"`
	FreeVCPU            int64           `json:"free_vcpu"`
	FreeRAMMB           int64           `json:"free_ram_mb"`
	FreeDiskGB          int64           `json:"free_disk_gb"`
	LastSeenAt          *time.Time      `json:"last_seen_at"`
	ClearanceHoldUntil  *time.Time      `json:"clearance_hold_until"`
	RetiredAt           *time.Time      `json:"retired_at"`
	RetiredReason       string          `json:"retired_reason,omitempty"`
	ActiveServices      int             `json:"active_services"`
	EscrowHoldingMinor  int64           `json:"escrow_holding_minor"`
	HostPendingMinor    int64           `json:"host_pending_minor"`
	HostReleasedMinor   int64           `json:"host_released_minor"`
	FeeMinor            int64           `json:"fee_minor"`
	CreatedAt           time.Time       `json:"created_at"`
	Plans               []Plan          `json:"plans"`
	Services            []HostedService `json:"services,omitempty"`
}

type HostedNodeInput struct {
	Name            string `json:"name"`
	RegionID        string `json:"region_id"`
	Location        string `json:"location"`
	LineDescription string `json:"line_description"`
	ExpiresAt       string `json:"expires_at"`
	TrafficQuotaGB  int    `json:"traffic_quota_gb"`
}

// Validate returns a customer-facing message, or "" when the input is fine.
func (in *HostedNodeInput) Validate() string {
	in.Name = strings.TrimSpace(in.Name)
	in.Location = strings.TrimSpace(in.Location)
	in.LineDescription = strings.TrimSpace(in.LineDescription)
	in.ExpiresAt = strings.TrimSpace(in.ExpiresAt)
	switch {
	case len([]rune(in.Name)) < 2 || len([]rune(in.Name)) > 40:
		return "母机名称需为 2-40 个字符"
	case len([]rune(in.Location)) < 2 || len([]rune(in.Location)) > 80:
		return "请填写真实的地理位置（2-80 个字符）"
	case len([]rune(in.LineDescription)) < 2 || len([]rune(in.LineDescription)) > 500:
		return "请填写线路描述（2-500 个字符）"
	case in.TrafficQuotaGB < 0 || in.TrafficQuotaGB > 10_000_000:
		return "月流量限额无效"
	}
	expires, err := time.Parse("2006-01-02", in.ExpiresAt)
	if err != nil || !expires.After(time.Now().UTC()) {
		return "请填写母机租约的真实到期日期（需晚于今天）"
	}
	return ""
}

// HostedNodeRegistration is a verified agent ready to become a hosted node.
type HostedNodeRegistration struct {
	BaseURL             string
	APIKeyCiphertext    []byte
	VirtualizationTypes []string
	Capacity            map[string]any
	CapacityVCPU        int64
	CapacityRAMMB       int64
	CapacityDiskGB      int64
}

func (m *MarketplaceStore) CreateHostedNode(ctx context.Context, ownerID, userID string, input HostedNodeInput, agent HostedNodeRegistration) (string, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var balance int64
	if err := tx.QueryRow(ctx, `SELECT balance_minor FROM accounts WHERE id=$1 AND status='active'`, ownerID).Scan(&balance); err != nil {
		return "", err
	}
	if balance < 0 {
		return "", ErrHostInDebt
	}
	capacity, _ := json.Marshal(agent.Capacity)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO nodes(region_id,name,provider_type,base_url,api_key_ciphertext,status,virtualization_types,capacity,
		                  capacity_vcpu,capacity_ram_mb,capacity_disk_gb,last_seen_at,owner_account_id,location,line_description,
		                  expires_at,traffic_quota_gb,listing_status)
		SELECT r.id,$2,'hatch',$3,$4,'online',$5,$6,$7,$8,$9,now(),$10,$11,$12,$13::date,$14,'listed'
		FROM regions r WHERE r.id=$1 AND r.enabled
		RETURNING id
	`, input.RegionID, input.Name, agent.BaseURL, agent.APIKeyCiphertext, agent.VirtualizationTypes, capacity,
		agent.CapacityVCPU, agent.CapacityRAMMB, agent.CapacityDiskGB, ownerID, input.Location, input.LineDescription,
		input.ExpiresAt, input.TrafficQuotaGB).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", ErrNodeNameTaken
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("region unavailable")
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',$1,'hosting.node_published','node',$2,jsonb_build_object('account_id',$3::text,'name',$4::text))`, userID, id, ownerID, input.Name); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (m *MarketplaceStore) UpdateHostedNode(ctx context.Context, ownerID, nodeID string, input HostedNodeInput) error {
	tag, err := m.db.Exec(ctx, `
		UPDATE nodes SET name=$3, location=$4, line_description=$5, expires_at=$6::date, traffic_quota_gb=$7, updated_at=now()
		WHERE id=$1 AND owner_account_id=$2 AND retired_at IS NULL
	`, nodeID, ownerID, input.Name, input.Location, input.LineDescription, input.ExpiresAt, input.TrafficQuotaGB)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrNodeNameTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrHostedNodeNotFound
	}
	return nil
}

// SetListing pauses or resumes new sales; running instances are unaffected.
// An empty owner is a staff action on any hosted node.
func (m *MarketplaceStore) SetListing(ctx context.Context, ownerID, nodeID string, listed bool) error {
	status := "paused"
	if listed {
		status = "listed"
	}
	tag, err := m.db.Exec(ctx, `UPDATE nodes SET listing_status=$3, updated_at=now() WHERE id=$1 AND owner_account_id IS NOT NULL AND ($2='' OR owner_account_id::text=$2) AND retired_at IS NULL`, nodeID, ownerID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrHostedNodeNotFound
	}
	return nil
}

// NodeOwner returns the owning account of a hosted node.
func (m *MarketplaceStore) NodeOwner(ctx context.Context, nodeID string) (string, bool, error) {
	var owner string
	var retired *time.Time
	err := m.db.QueryRow(ctx, `SELECT owner_account_id::text, retired_at FROM nodes WHERE id=$1 AND owner_account_id IS NOT NULL`, nodeID).Scan(&owner, &retired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrHostedNodeNotFound
	}
	return owner, retired != nil, err
}

// HostedNodes lists hosted nodes with their plans and money figures. An
// empty owner lists every host (staff view); marketOnly keeps what buyers
// may see: listed, not retired, with sellable plans.
func (m *MarketplaceStore) HostedNodes(ctx context.Context, ownerID string, marketOnly, withServices bool) ([]HostedNode, error) {
	where := "n.owner_account_id IS NOT NULL"
	args := []any{}
	if ownerID != "" {
		args = append(args, ownerID)
		where += fmt.Sprintf(" AND n.owner_account_id=$%d", len(args))
	}
	if marketOnly {
		where += " AND n.retired_at IS NULL AND n.listing_status='listed'"
	}
	rows, err := m.db.Query(ctx, `
		SELECT n.id,n.name,n.owner_account_id,a.display_name,a.billing_email,a.balance_minor,n.region_id,r.name,n.location,n.line_description,
		       n.status,n.listing_status,n.virtualization_types,coalesce(to_char(n.expires_at,'YYYY-MM-DD'),''),n.traffic_quota_gb,
		       n.capacity_vcpu,n.capacity_ram_mb,n.capacity_disk_gb,
		       coalesce(res.vcpu,0),coalesce(res.ram_mb,0),coalesce(res.disk_gb,0),
		       n.last_seen_at,n.clearance_hold_until,n.retired_at,coalesce(n.retired_reason,''),
		       (SELECT count(*) FROM services s JOIN plans p ON p.id=s.plan_id WHERE p.node_id=n.id AND s.status IN ('provisioning','active','overdue','suspended'))::int,
		       coalesce(e.holding,0),coalesce(e.host_pending,0),coalesce(e.host_released,0),coalesce(e.fee,0),n.created_at
		FROM nodes n JOIN accounts a ON a.id=n.owner_account_id JOIN regions r ON r.id=n.region_id
		LEFT JOIN LATERAL (SELECT sum(vcpu) vcpu, sum(ram_mb) ram_mb, sum(disk_gb) disk_gb FROM inventory_reservations WHERE node_id=n.id AND status='reserved') res ON true
		LEFT JOIN LATERAL (
			SELECT sum(CASE WHEN status='holding' THEN gross_minor-released_gross_minor ELSE 0 END)::bigint holding,
			       sum(CASE WHEN status='holding' THEN host_share_minor-released_host_minor ELSE 0 END)::bigint host_pending,
			       sum(released_host_minor)::bigint host_released,
			       sum(CASE WHEN status IN ('cleared','refunded') THEN (fee_minor*released_gross_minor)/greatest(gross_minor,1) ELSE fee_minor END)::bigint fee
			FROM marketplace_escrows WHERE node_id=n.id) e ON true
		WHERE `+where+` ORDER BY n.retired_at NULLS FIRST, n.created_at DESC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]HostedNode, 0)
	for rows.Next() {
		var node HostedNode
		var reservedVCPU, reservedRAM, reservedDisk int64
		if err := rows.Scan(&node.ID, &node.Name, &node.OwnerAccountID, &node.OwnerName, &node.OwnerEmail, &node.OwnerBalanceMinor, &node.RegionID, &node.RegionName, &node.Location, &node.LineDescription,
			&node.Status, &node.ListingStatus, &node.VirtualizationTypes, &node.ExpiresAt, &node.TrafficQuotaGB,
			&node.CapacityVCPU, &node.CapacityRAMMB, &node.CapacityDiskGB, &reservedVCPU, &reservedRAM, &reservedDisk,
			&node.LastSeenAt, &node.ClearanceHoldUntil, &node.RetiredAt, &node.RetiredReason, &node.ActiveServices,
			&node.EscrowHoldingMinor, &node.HostPendingMinor, &node.HostReleasedMinor, &node.FeeMinor, &node.CreatedAt); err != nil {
			return nil, err
		}
		node.FreeVCPU = max(node.CapacityVCPU-reservedVCPU, 0)
		node.FreeRAMMB = max(node.CapacityRAMMB-reservedRAM, 0)
		node.FreeDiskGB = max(node.CapacityDiskGB-reservedDisk, 0)
		node.Plans = []Plan{}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nodes, nil
	}
	plans, err := NewCatalogStore(m.db).ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i := range nodes {
		index[nodes[i].ID] = i
	}
	for _, plan := range plans {
		i, ok := index[plan.NodeID]
		if !ok || (marketOnly && (!plan.Enabled || len(plan.Prices) == 0)) {
			continue
		}
		nodes[i].Plans = append(nodes[i].Plans, plan)
	}
	if marketOnly {
		listed := nodes[:0]
		for _, node := range nodes {
			if len(node.Plans) > 0 {
				node.OwnerEmail, node.OwnerBalanceMinor = "", 0
				node.EscrowHoldingMinor, node.HostPendingMinor, node.HostReleasedMinor, node.FeeMinor = 0, 0, 0, 0
				listed = append(listed, node)
			}
		}
		nodes = listed
	}
	if withServices {
		for i := range nodes {
			if nodes[i].Services, err = m.hostedServices(ctx, nodes[i].ID); err != nil {
				return nil, err
			}
		}
	}
	return nodes, nil
}

func (m *MarketplaceStore) hostedServices(ctx context.Context, nodeID string) ([]HostedService, error) {
	rows, err := m.db.Query(ctx, `
		SELECT s.id,s.instance_name,p.name,s.status,s.runtime_status,a.display_name,s.next_due_at,s.created_at,
		       coalesce((SELECT sum(gross_minor-released_gross_minor) FROM marketplace_escrows e WHERE e.service_id=s.id AND e.status='holding'),0)::bigint
		FROM services s JOIN plans p ON p.id=s.plan_id JOIN accounts a ON a.id=s.account_id
		WHERE p.node_id=$1 ORDER BY (s.status='terminated'), s.created_at DESC LIMIT 200
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]HostedService, 0)
	for rows.Next() {
		var row HostedService
		if err := rows.Scan(&row.ID, &row.InstanceName, &row.PlanName, &row.Status, &row.RuntimeStatus, &row.BuyerName, &row.NextDueAt, &row.CreatedAt, &row.RemainingValueMinor); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// HostedPlanOwner returns the owner and node of a hosted plan.
func (m *MarketplaceStore) HostedPlanOwner(ctx context.Context, planID string) (owner, nodeID string, err error) {
	err = m.db.QueryRow(ctx, `SELECT owner_account_id::text, node_id::text FROM plans WHERE id=$1 AND owner_account_id IS NOT NULL`, planID).Scan(&owner, &nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrHostedNodeNotFound
	}
	return owner, nodeID, err
}

// ReleaseEscrows credits hosts with the days of service that have passed.
// Services that never became usable keep their money in escrow.
func (m *MarketplaceStore) ReleaseEscrows(ctx context.Context, now time.Time) (int, error) {
	rows, err := m.db.Query(ctx, `
		SELECT e.id FROM marketplace_escrows e JOIN services s ON s.id=e.service_id
		WHERE e.status='holding' AND s.status IN ('active','overdue','suspended')
		  AND e.period_start <= $1::timestamptz - interval '1 day'
		ORDER BY e.period_start LIMIT 500
	`, now)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	released := 0
	for _, id := range ids {
		ok, err := m.releaseEscrow(ctx, id, now)
		if err != nil {
			return released, err
		}
		if ok {
			released++
		}
	}
	return released, nil
}

// EscrowDays splits a paid period into whole days (at least one) and
// counts the days elapsed at now.
func EscrowDays(start, end, now time.Time) (total, elapsed int) {
	total = int(math.Ceil(end.Sub(start).Hours() / 24))
	if total < 1 {
		total = 1
	}
	if !now.Before(end) {
		return total, total
	}
	elapsed = int(now.Sub(start).Hours() / 24)
	return total, min(max(elapsed, 0), total)
}

func (m *MarketplaceStore) releaseEscrow(ctx context.Context, id string, now time.Time) (bool, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var host, status, instance string
	var gross, share, releasedHost int64
	var releasedDays int
	var start, end time.Time
	if err := tx.QueryRow(ctx, `
		SELECT e.host_account_id,e.status,s.instance_name,e.gross_minor,e.host_share_minor,e.released_host_minor,e.released_days,e.period_start,e.period_end
		FROM marketplace_escrows e JOIN services s ON s.id=e.service_id WHERE e.id=$1 FOR UPDATE OF e
	`, id).Scan(&host, &status, &instance, &gross, &share, &releasedHost, &releasedDays, &start, &end); err != nil {
		return false, err
	}
	total, elapsed := EscrowDays(start, end, now)
	if status != "holding" || elapsed <= releasedDays {
		return false, nil
	}
	targetHost := share * int64(elapsed) / int64(total)
	targetGross := gross * int64(elapsed) / int64(total)
	if delta := targetHost - releasedHost; delta > 0 {
		if _, err := applyWalletChange(ctx, tx, walletChange{
			AccountID: host, Kind: "earning", AmountMinor: delta,
			Description:   fmt.Sprintf("托管收益：%s 第 %d-%d 天（共 %d 天）", instance, releasedDays+1, elapsed, total),
			ReferenceType: "escrow", ReferenceID: id, DedupKey: fmt.Sprintf("escrow:%s:day:%d", id, elapsed),
		}); err != nil {
			return false, err
		}
	}
	next := "holding"
	if elapsed >= total {
		next = "released"
	}
	if _, err := tx.Exec(ctx, `UPDATE marketplace_escrows SET released_days=$2,released_gross_minor=$3,released_host_minor=$4,status=$5,updated_at=now() WHERE id=$1`, id, elapsed, targetGross, targetHost, next); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

type ClearedService struct {
	ServiceID      string `json:"service_id"`
	InstanceName   string `json:"instance_name"`
	BuyerName      string `json:"buyer_name"`
	BuyerEmail     string `json:"-"`
	RemainingMinor int64  `json:"remaining_minor"`
	RefundMinor    int64  `json:"refund_minor"`
	PenaltyMinor   int64  `json:"penalty_minor"`
}

type ClearanceResult struct {
	NodeID       string           `json:"node_id"`
	NodeName     string           `json:"node_name"`
	HostName     string           `json:"host_name"`
	HostEmail    string           `json:"-"`
	Multiplier   int              `json:"multiplier"`
	Reason       string           `json:"reason"`
	Currency     string           `json:"currency"`
	Services     []ClearedService `json:"services"`
	PenaltyMinor int64            `json:"penalty_minor"`
	RefundMinor  int64            `json:"refund_minor"`
}

// ClearNode retires a hosted node and settles every instance on it: the
// buyer gets multiplier x the remaining value, the unreleased escrow covers
// one share and the host pays the rest from the balance (which may go
// negative). Instances are closed without contacting the node, which is
// usually offline at this point.
func (m *MarketplaceStore) ClearNode(ctx context.Context, nodeID string, multiplier int, reason, actorType, actorID string) (ClearanceResult, error) {
	reason = strings.TrimSpace(reason)
	if multiplier != 1 && multiplier != 2 {
		return ClearanceResult{}, errors.New("multiplier must be 1 or 2")
	}
	if reason == "" {
		reason = "母机清退"
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return ClearanceResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := ClearanceResult{NodeID: nodeID, Multiplier: multiplier, Reason: reason, Services: []ClearedService{}}
	var host string
	var retired *time.Time
	err = tx.QueryRow(ctx, `
		SELECT n.name,n.owner_account_id,n.retired_at,a.display_name,a.billing_email,a.default_currency
		FROM nodes n JOIN accounts a ON a.id=n.owner_account_id WHERE n.id=$1 FOR UPDATE OF n
	`, nodeID).Scan(&result.NodeName, &host, &retired, &result.HostName, &result.HostEmail, &result.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClearanceResult{}, ErrHostedNodeNotFound
	}
	if err != nil {
		return ClearanceResult{}, err
	}
	if retired != nil {
		return ClearanceResult{}, ErrNodeRetired
	}
	rows, err := tx.Query(ctx, `
		SELECT s.id,s.instance_name,s.account_id,a.display_name,a.billing_email
		FROM services s JOIN plans p ON p.id=s.plan_id JOIN accounts a ON a.id=s.account_id
		WHERE p.node_id=$1 AND s.status NOT IN ('terminated') AND s.refunded_at IS NULL ORDER BY s.created_at FOR UPDATE OF s
	`, nodeID)
	if err != nil {
		return ClearanceResult{}, err
	}
	type affected struct {
		ClearedService
		buyer string
	}
	items := make([]affected, 0)
	for rows.Next() {
		var item affected
		if err := rows.Scan(&item.ServiceID, &item.InstanceName, &item.buyer, &item.BuyerName, &item.BuyerEmail); err != nil {
			rows.Close()
			return ClearanceResult{}, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(gross_minor-released_gross_minor),0)::bigint FROM marketplace_escrows WHERE service_id=$1 AND status='holding'`, item.ServiceID).Scan(&item.RemainingMinor); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE marketplace_escrows SET status='cleared',cleared_at=now(),refunded_minor=(gross_minor-released_gross_minor)*$2,updated_at=now() WHERE service_id=$1 AND status='holding'`, item.ServiceID, multiplier); err != nil {
			return ClearanceResult{}, err
		}
		item.RefundMinor = item.RemainingMinor * int64(multiplier)
		item.PenaltyMinor = item.RemainingMinor * int64(multiplier-1)
		if _, err := applyWalletChange(ctx, tx, walletChange{
			AccountID: item.buyer, Kind: "clearance_refund", AmountMinor: item.RefundMinor,
			Description:   fmt.Sprintf("母机 %s 清退补偿：实例 %s（剩余价值 ×%d）", result.NodeName, item.InstanceName, multiplier),
			ReferenceType: "service", ReferenceID: item.ServiceID, DedupKey: "clearance:" + item.ServiceID + ":refund",
		}); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := applyWalletChange(ctx, tx, walletChange{
			AccountID: host, Kind: "clearance_penalty", AmountMinor: -item.PenaltyMinor, AllowNegative: true,
			Description:   fmt.Sprintf("母机 %s 清退赔付：实例 %s", result.NodeName, item.InstanceName),
			ReferenceType: "service", ReferenceID: item.ServiceID, DedupKey: "clearance:" + item.ServiceID + ":penalty",
		}); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE services SET status='terminated',termination_reason=$2,runtime_status='stopped',desired_runtime_status=NULL,terminated_at=now(),updated_at=now() WHERE id=$1`, item.ServiceID, "母机清退："+reason); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE inventory_reservations SET status='released',released_at=now() WHERE service_id=$1 AND status='reserved'`, item.ServiceID); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status='void',updated_at=now() WHERE service_id=$1 AND status='open'`, item.ServiceID); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE provisioning_jobs SET status='dead',locked_at=NULL,locked_by=NULL,last_error='hosted node cleared',updated_at=now() WHERE service_id=$1 AND status IN ('pending','failed')`, item.ServiceID); err != nil {
			return ClearanceResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.cleared',$2,jsonb_build_object('node_id',$3::text,'refund_minor',$4::bigint)) ON CONFLICT(deduplication_key) DO NOTHING`, item.ServiceID, item.ServiceID+":cleared:v1", nodeID, item.RefundMinor); err != nil {
			return ClearanceResult{}, err
		}
		result.RefundMinor += item.RefundMinor
		result.PenaltyMinor += item.PenaltyMinor
		result.Services = append(result.Services, item.ClearedService)
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET listing_status='retired',retired_at=now(),retired_reason=$2,status='maintenance',updated_at=now() WHERE id=$1`, nodeID, reason); err != nil {
		return ClearanceResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE plans SET enabled=false,updated_at=now() WHERE node_id=$1`, nodeID); err != nil {
		return ClearanceResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES($1,nullif($2,'')::uuid,'hosting.node_cleared','node',$3,jsonb_build_object('multiplier',$4::int,'reason',$5::text,'services',$6::int,'refund_minor',$7::bigint,'penalty_minor',$8::bigint))`,
		actorType, actorID, nodeID, multiplier, reason, len(result.Services), result.RefundMinor, result.PenaltyMinor); err != nil {
		return ClearanceResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ClearanceResult{}, err
	}
	return result, nil
}

// HoldClearance postpones the automatic clearance of an offline node until
// the given time; nil lifts the hold.
func (m *MarketplaceStore) HoldClearance(ctx context.Context, nodeID, staffID string, until *time.Time) error {
	tag, err := m.db.Exec(ctx, `UPDATE nodes SET clearance_hold_until=$2,updated_at=now() WHERE id=$1 AND owner_account_id IS NOT NULL AND retired_at IS NULL`, nodeID, until)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrHostedNodeNotFound
	}
	_, err = m.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,'hosting.clearance_hold','node',$2,jsonb_build_object('until',$3::timestamptz))`, staffID, nodeID, until)
	return err
}

type OfflineHostedNode struct {
	ID                string
	Name              string
	OwnerName         string
	OwnerEmail        string
	LastSeenAt        time.Time
	HoldUntil         *time.Time
	OfflineNotifiedAt *time.Time
}

// OfflineHostedNodes lists hosted nodes that have been unreachable for at
// least the given duration.
func (m *MarketplaceStore) OfflineHostedNodes(ctx context.Context, since time.Duration) ([]OfflineHostedNode, error) {
	rows, err := m.db.Query(ctx, `
		SELECT n.id,n.name,a.display_name,a.billing_email,coalesce(n.last_seen_at,n.created_at),n.clearance_hold_until,n.offline_notified_at
		FROM nodes n JOIN accounts a ON a.id=n.owner_account_id
		WHERE n.owner_account_id IS NOT NULL AND n.retired_at IS NULL AND n.status<>'online'
		  AND coalesce(n.last_seen_at,n.created_at) <= now() - make_interval(secs => $1)
	`, since.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OfflineHostedNode, 0)
	for rows.Next() {
		var row OfflineHostedNode
		if err := rows.Scan(&row.ID, &row.Name, &row.OwnerName, &row.OwnerEmail, &row.LastSeenAt, &row.HoldUntil, &row.OfflineNotifiedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (m *MarketplaceStore) MarkOfflineNotified(ctx context.Context, nodeID string) error {
	_, err := m.db.Exec(ctx, `UPDATE nodes SET offline_notified_at=now() WHERE id=$1`, nodeID)
	return err
}
