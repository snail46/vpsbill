package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultTradeHoldDays is how long an owner must have held an instance
// before listing it in the trading market, unless the site settings say
// otherwise (system_settings.trade_hold_days).
const DefaultTradeHoldDays = 31

// TradeHoldDays reads the holding period from the site settings.
func TradeHoldDays(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) int {
	days := DefaultTradeHoldDays
	_ = db.QueryRow(ctx, `SELECT trade_hold_days FROM system_settings WHERE singleton=true`).Scan(&days)
	return days
}

// TradeMinRemainingDays is how much paid time an instance must have left to
// stay listed; listings closer to expiry are withdrawn.
const TradeMinRemainingDays = 3

// ListingTraffic is an instance's traffic when it was listed. The instance
// is stopped while listed, so buyers see what the seller used.
type ListingTraffic struct {
	TotalBytes int64
	RXBytes    *int64
	TXBytes    *int64
}

var (
	ErrListingNotFound = errors.New("listing not found")
	ErrListingChanged  = errors.New("listing price changed")
)

// TradeError explains why an instance cannot be listed or bought.
type TradeError struct{ Message string }

func (e *TradeError) Error() string { return e.Message }

type TradeStore struct{ db *pgxpool.Pool }

func NewTradeStore(db *pgxpool.Pool) *TradeStore { return &TradeStore{db: db} }

// TradeListing is an instance offered for sale. Buyers see the instance's
// specs and terms, never its address or credentials.
type TradeListing struct {
	ID               string `json:"id"`
	ServiceID        string `json:"service_id,omitempty"`
	InstanceName     string `json:"instance_name,omitempty"`
	SellerName       string `json:"seller_name"`
	Mine             bool   `json:"mine"`
	Status           string `json:"status"`
	Available        bool   `json:"available"`
	CancelReason     string `json:"cancel_reason,omitempty"`
	PriceMinor       int64  `json:"price_minor"`
	Currency         string `json:"currency"`
	Note             string `json:"note"`
	PlanName         string `json:"plan_name"`
	Virtualization   string `json:"virtualization"`
	VCPU             int    `json:"vcpu"`
	RAMMB            int    `json:"ram_mb"`
	DiskGB           int    `json:"disk_gb"`
	TrafficGB        int    `json:"traffic_gb"`
	NetworkDownMbps  int    `json:"network_down_mbps"`
	PortMappingCount int    `json:"port_mapping_count"`
	RegionName       string `json:"region_name"`
	HostName         string `json:"host_name,omitempty"`
	NodeOnline       bool   `json:"node_online"`
	// Traffic this month when the instance was listed; RX/TX are nil when
	// the backend reports only a total.
	TrafficBytes     int64      `json:"traffic_bytes"`
	TrafficRXBytes   *int64     `json:"traffic_rx_bytes"`
	TrafficTXBytes   *int64     `json:"traffic_tx_bytes"`
	FeeMinor         *int64     `json:"fee_minor,omitempty"`
	ProceedsMinor    *int64     `json:"seller_proceeds_minor,omitempty"`
	BillingCycle     string     `json:"billing_cycle"`
	ExpiresAt        *time.Time `json:"expires_at"`
	RenewalMinor     *int64     `json:"renewal_minor"`
	ServiceCreatedAt time.Time  `json:"service_created_at"`
	CreatedAt        time.Time  `json:"created_at"`
	SoldAt           *time.Time `json:"sold_at,omitempty"`
	// ClosedBy says who closed a cancelled listing: seller, staff, system
	// or expiry; UpdatedAt is when the listing last changed.
	ClosedBy  string    `json:"cancelled_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Who sees a listing decides what of it they see.
type listingViewer int

const (
	// marketViewer browses the market: no instance name, masked seller.
	marketViewer listingViewer = iota
	// sellerViewer sees everything about their own listings.
	sellerViewer
	// buyerViewer bought the instance: it is theirs now, but the seller
	// stays masked and the seller's proceeds private.
	buyerViewer
)

// maskName keeps the first character of a display name.
func maskName(name string) string {
	runes := []rune(strings.TrimSpace(name))
	if len(runes) == 0 {
		return "用户"
	}
	return string(runes[0]) + "**"
}

const listingSelect = `
	SELECT l.id,l.service_id,s.instance_name,l.seller_account_id,sa.display_name,l.status,coalesce(l.cancel_reason,''),l.price_minor,l.currency,l.note,
	       p.name,p.virtualization,p.vcpu,p.ram_mb,p.disk_gb,p.traffic_gb,p.network_down_mbps,p.port_mapping_count,r.name,coalesce(h.display_name,''),
	       s.billing_cycle,s.next_due_at,s.created_at,l.created_at,l.sold_at,
	       s.status='active' AND s.account_id=l.seller_account_id,
	       least(coalesce((SELECT pp.amount_minor FROM plan_prices pp WHERE pp.plan_id=p.id AND pp.currency=l.currency AND pp.billing_cycle=s.billing_cycle
	          AND pp.active_from<=now() AND (pp.active_until IS NULL OR pp.active_until>now()) ORDER BY pp.active_from DESC LIMIT 1),s.list_price_minor),s.renewal_price_minor),
	       s.renewal_discount_type,s.renewal_discount_value,coalesce(n.status='online',false),
	       coalesce(l.traffic_total_bytes,0),l.traffic_rx_bytes,l.traffic_tx_bytes,l.fee_minor,l.seller_proceeds_minor,l.cancelled_by,l.updated_at
	FROM service_listings l JOIN services s ON s.id=l.service_id JOIN plans p ON p.id=s.plan_id JOIN regions r ON r.id=s.region_id
	JOIN accounts sa ON sa.id=l.seller_account_id LEFT JOIN accounts h ON h.id=p.owner_account_id LEFT JOIN nodes n ON n.id=s.node_id`

func scanListings(rows pgx.Rows, viewer string, as listingViewer) ([]TradeListing, error) {
	defer rows.Close()
	result := make([]TradeListing, 0)
	for rows.Next() {
		var l TradeListing
		var seller string
		var discountType *string
		var discountValue *int64
		if err := rows.Scan(&l.ID, &l.ServiceID, &l.InstanceName, &seller, &l.SellerName, &l.Status, &l.CancelReason, &l.PriceMinor, &l.Currency, &l.Note,
			&l.PlanName, &l.Virtualization, &l.VCPU, &l.RAMMB, &l.DiskGB, &l.TrafficGB, &l.NetworkDownMbps, &l.PortMappingCount, &l.RegionName, &l.HostName,
			&l.BillingCycle, &l.ExpiresAt, &l.ServiceCreatedAt, &l.CreatedAt, &l.SoldAt, &l.Available, &l.RenewalMinor, &discountType, &discountValue, &l.NodeOnline,
			&l.TrafficBytes, &l.TrafficRXBytes, &l.TrafficTXBytes, &l.FeeMinor, &l.ProceedsMinor, &l.ClosedBy, &l.UpdatedAt); err != nil {
			return nil, err
		}
		if l.RenewalMinor != nil && discountType != nil && discountValue != nil {
			renewal := *l.RenewalMinor - CouponDiscount(*discountType, *discountValue, *l.RenewalMinor)
			l.RenewalMinor = &renewal
		}
		l.Mine = seller == viewer
		switch as {
		case marketViewer:
			l.ServiceID, l.InstanceName, l.FeeMinor, l.ProceedsMinor = "", "", nil, nil
			l.SellerName = maskName(l.SellerName)
		case buyerViewer:
			l.FeeMinor, l.ProceedsMinor = nil, nil
			l.SellerName = maskName(l.SellerName)
		}
		if l.Status != "cancelled" {
			l.ClosedBy = ""
		}
		result = append(result, l)
	}
	return result, rows.Err()
}

// Market lists open listings whose instance is still running under the seller.
func (s *TradeStore) Market(ctx context.Context, viewer string) ([]TradeListing, error) {
	rows, err := s.db.Query(ctx, listingSelect+`
		WHERE l.status='listed' AND s.status='active' AND s.account_id=l.seller_account_id
		ORDER BY l.created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	return scanListings(rows, viewer, marketViewer)
}

// SellerListings lists a seller's listings, open ones first.
func (s *TradeStore) SellerListings(ctx context.Context, seller string) ([]TradeListing, error) {
	rows, err := s.db.Query(ctx, listingSelect+`
		WHERE l.seller_account_id=$1 ORDER BY (l.status='listed') DESC, l.created_at DESC LIMIT 200`, seller)
	if err != nil {
		return nil, err
	}
	return scanListings(rows, seller, sellerViewer)
}

// Purchases lists the instances an account bought in the market.
func (s *TradeStore) Purchases(ctx context.Context, buyer string) ([]TradeListing, error) {
	rows, err := s.db.Query(ctx, listingSelect+`
		WHERE l.buyer_account_id=$1 AND l.status='sold' ORDER BY l.sold_at DESC LIMIT 200`, buyer)
	if err != nil {
		return nil, err
	}
	return scanListings(rows, buyer, buyerViewer)
}

// AllListings is the staff view.
func (s *TradeStore) AllListings(ctx context.Context) ([]TradeListing, error) {
	rows, err := s.db.Query(ctx, listingSelect+` ORDER BY (l.status='listed') DESC, l.created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	return scanListings(rows, "", sellerViewer)
}

// CreateListing offers the seller's instance for sale.
// TradeTarget returns the node and instance of a seller's service so the
// caller can read its traffic before listing it.
func (s *TradeStore) TradeTarget(ctx context.Context, seller, serviceID string) (nodeID, instance string, err error) {
	var node *string
	err = s.db.QueryRow(ctx, `SELECT s.node_id::text,s.instance_name FROM services s WHERE s.id=$1 AND s.account_id=$2`, serviceID, seller).Scan(&node, &instance)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrServiceNotFound
	}
	if node != nil {
		nodeID = *node
	}
	return nodeID, instance, err
}

// CreateListing offers the seller's instance for sale. The instance is
// stopped and frozen for the seller until it sells or is withdrawn; its
// paid time keeps running.
func (s *TradeStore) CreateListing(ctx context.Context, seller, userID, serviceID string, price int64, note string, traffic *ListingTraffic) (string, error) {
	note = strings.TrimSpace(note)
	switch {
	case price < 100 || price > 10_000_000:
		return "", &TradeError{"挂售价格需在 1 到 100000 之间"}
	case len([]rune(note)) > 500:
		return "", &TradeError{"说明不能超过 500 个字符"}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, currency, runtimeStatus, desired, lockedMonth string
	var acquired time.Time
	var refunded, nextDue *time.Time
	var nodeOnline bool
	err = tx.QueryRow(ctx, `
		SELECT s.status,s.acquired_at,s.refunded_at,a.default_currency,s.next_due_at,s.runtime_status,coalesce(s.desired_runtime_status,''),
		       coalesce(s.traffic_locked_month,''),coalesce((SELECT n.status='online' FROM nodes n WHERE n.id=s.node_id),false)
		FROM services s JOIN accounts a ON a.id=s.account_id
		WHERE s.id=$1 AND s.account_id=$2 FOR UPDATE OF s
	`, serviceID, seller).Scan(&status, &acquired, &refunded, &currency, &nextDue, &runtimeStatus, &desired, &lockedMonth, &nodeOnline)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrServiceNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "active" || refunded != nil {
		return "", &TradeError{"只有正常运行中的实例可以挂售"}
	}
	holdDays := TradeHoldDays(ctx, tx)
	if wait := time.Until(acquired.AddDate(0, 0, holdDays)); wait > 0 {
		return "", &TradeError{fmt.Sprintf("持有满 %d 天的实例才能挂售，这台实例还需 %d 天", holdDays, int(math.Ceil(wait.Hours()/24)))}
	}
	switch {
	case nextDue == nil || time.Until(*nextDue) < TradeMinRemainingDays*24*time.Hour:
		return "", &TradeError{fmt.Sprintf("距到期不足 %d 天的实例不能挂售，请先续费", TradeMinRemainingDays)}
	case !nodeOnline:
		return "", &TradeError{"实例所在节点当前离线，暂不能挂售"}
	case desired != "":
		return "", &TradeError{"实例正在执行开关机操作，请稍后再挂售"}
	case lockedMonth != "":
		return "", &TradeError{"实例因流量用尽已停止，下月恢复后才能挂售"}
	case traffic == nil:
		return "", &TradeError{"暂时读不到实例流量，请稍后再试"}
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO service_listings(service_id,seller_account_id,currency,price_minor,note,traffic_total_bytes,traffic_rx_bytes,traffic_tx_bytes,traffic_measured_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()) RETURNING id`,
		serviceID, seller, currency, price, note, traffic.TotalBytes, traffic.RXBytes, traffic.TXBytes).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", &TradeError{"这台实例已经在挂售中"}
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE services SET traffic_used_bytes=$2,traffic_measured_at=now() WHERE id=$1`, serviceID, traffic.TotalBytes); err != nil {
		return "", err
	}
	// Freeze the instance: stop it so its traffic and data stay as listed.
	if runtimeStatus != "stopped" {
		if _, err := tx.Exec(ctx, `UPDATE services SET desired_runtime_status='stopped',updated_at=now() WHERE id=$1`, serviceID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'stop',$2,jsonb_build_object('source','trade_listing')) ON CONFLICT(deduplication_key) DO NOTHING`, serviceID, serviceID+":trade-stop:"+id); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',nullif($1,'')::uuid,'trade.listed','service',$2,jsonb_build_object('listing_id',$3::text,'price_minor',$4::bigint))`,
		userID, serviceID, id, price); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// CancelListing withdraws an open listing; an empty seller lets staff
// cancel any listing.
func (s *TradeStore) CancelListing(ctx context.Context, seller, actorType, actorID, listingID, reason string) error {
	command, err := s.db.Exec(ctx, `
		UPDATE service_listings SET status='cancelled',cancel_reason=nullif($3,''),cancelled_by=CASE WHEN $2='' THEN 'staff' ELSE 'seller' END,updated_at=now()
		WHERE id=$1 AND status='listed' AND ($2='' OR seller_account_id::text=$2)
	`, listingID, seller, strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrListingNotFound
	}
	_, err = s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES($1,nullif($2,'')::uuid,'trade.cancelled','listing',$3,jsonb_build_object('reason',$4::text))`,
		actorType, actorID, listingID, reason)
	return err
}

// TradeResult is a completed sale, with contacts for the notices.
type TradeResult struct {
	ListingID    string
	ServiceID    string
	InstanceName string
	PlanName     string
	PriceMinor   int64
	FeeMinor     int64
	FeePercent   float64
	Currency     string
	SellerName   string
	SellerEmail  string
	BuyerName    string
	BuyerEmail   string
}

// Buy moves a listed instance to the buyer, paid from the buyer's balance
// to the seller's. The buyer confirms the price they were shown.
func (s *TradeStore) Buy(ctx context.Context, buyer, userID, listingID string, expectedMinor int64) (TradeResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TradeResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result TradeResult
	var seller, status, serviceStatus, owner, buyerCurrency string
	var planOwner *string
	var nodeOnline bool
	err = tx.QueryRow(ctx, `
		SELECT l.id,l.service_id,l.seller_account_id,l.status,l.price_minor,l.currency,s.instance_name,s.status,s.account_id,p.name,p.owner_account_id,
		       coalesce((SELECT n.status='online' FROM nodes n WHERE n.id=s.node_id),false)
		FROM service_listings l JOIN services s ON s.id=l.service_id JOIN plans p ON p.id=s.plan_id
		WHERE l.id=$1 FOR UPDATE OF l, s
	`, listingID).Scan(&result.ListingID, &result.ServiceID, &seller, &status, &result.PriceMinor, &result.Currency, &result.InstanceName, &serviceStatus, &owner, &result.PlanName, &planOwner, &nodeOnline)
	if errors.Is(err, pgx.ErrNoRows) {
		return TradeResult{}, ErrListingNotFound
	}
	if err != nil {
		return TradeResult{}, err
	}
	switch {
	case status != "listed":
		return TradeResult{}, &TradeError{"这件商品已售出或已下架"}
	case seller == buyer:
		return TradeResult{}, &TradeError{"不能购买自己挂售的实例"}
	case planOwner != nil && *planOwner == buyer:
		return TradeResult{}, &TradeError{"不能购买自己托管母机上的实例"}
	case serviceStatus != "active" || owner != seller:
		if _, err := tx.Exec(ctx, `UPDATE service_listings SET status='cancelled',cancel_reason='实例已不是正常运行状态',cancelled_by='system',updated_at=now() WHERE id=$1`, listingID); err != nil {
			return TradeResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TradeResult{}, err
		}
		return TradeResult{}, &TradeError{"该实例已不是正常运行状态，挂售已自动下架"}
	case !nodeOnline:
		return TradeResult{}, &TradeError{"实例所在母机当前离线，暂不能购买"}
	case result.PriceMinor != expectedMinor:
		return TradeResult{}, ErrListingChanged
	}
	if err := tx.QueryRow(ctx, `SELECT default_currency FROM accounts WHERE id=$1 AND status='active'`, buyer).Scan(&buyerCurrency); err != nil {
		return TradeResult{}, err
	}
	if !strings.EqualFold(buyerCurrency, result.Currency) {
		return TradeResult{}, &TradeError{"账户币种与挂售币种不一致"}
	}
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT trade_fee_percent::float8 FROM system_settings WHERE singleton=true), 20)`).Scan(&result.FeePercent); err != nil {
		return TradeResult{}, err
	}
	result.FeeMinor = int64(math.Round(float64(result.PriceMinor) * result.FeePercent / 100))
	if _, err := applyWalletChange(ctx, tx, walletChange{
		AccountID: buyer, Kind: "trade_purchase", AmountMinor: -result.PriceMinor,
		Description:   fmt.Sprintf("交易市场购买实例 %s（%s）", result.InstanceName, result.PlanName),
		ReferenceType: "listing", ReferenceID: listingID, DedupKey: "trade:" + listingID + ":buyer", ActorUserID: userID,
	}); err != nil {
		return TradeResult{}, err
	}
	if _, err := applyWalletChange(ctx, tx, walletChange{
		AccountID: seller, Kind: "trade_sale", AmountMinor: result.PriceMinor - result.FeeMinor,
		Description:   fmt.Sprintf("交易市场售出实例 %s（%s），成交价 %.2f，扣除 %g%% 手续费", result.InstanceName, result.PlanName, float64(result.PriceMinor)/100, result.FeePercent),
		ReferenceType: "listing", ReferenceID: listingID, DedupKey: "trade:" + listingID + ":seller",
	}); err != nil {
		return TradeResult{}, err
	}
	// The seller knew the root password; clear it so the buyer resets it.
	if _, err := tx.Exec(ctx, `UPDATE services SET account_id=$2,acquired_at=now(),root_password_ciphertext=NULL,updated_at=now() WHERE id=$1`, result.ServiceID, buyer); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invoices SET account_id=$2,updated_at=now() WHERE service_id=$1 AND status='open'`, result.ServiceID, buyer); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE marketplace_escrows SET buyer_account_id=$2,updated_at=now() WHERE service_id=$1 AND status='holding'`, result.ServiceID, buyer); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE service_listings SET status='sold',buyer_account_id=$2,sold_at=now(),fee_percent=$3,fee_minor=$4,seller_proceeds_minor=$5,updated_at=now() WHERE id=$1`,
		listingID, buyer, result.FeePercent, result.FeeMinor, result.PriceMinor-result.FeeMinor); err != nil {
		return TradeResult{}, err
	}
	// The instance was stopped while listed; start it for its new owner.
	if _, err := tx.Exec(ctx, `UPDATE services SET desired_runtime_status='running' WHERE id=$1`, result.ServiceID); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'start',$2,jsonb_build_object('source','trade_sale')) ON CONFLICT(deduplication_key) DO NOTHING`, result.ServiceID, result.ServiceID+":trade-start:"+listingID); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.transferred',$2,jsonb_build_object('listing_id',$3::text,'from',$4::text,'to',$5::text,'price_minor',$6::bigint))`,
		result.ServiceID, "trade:"+listingID, listingID, seller, buyer, result.PriceMinor); err != nil {
		return TradeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',nullif($1,'')::uuid,'trade.sold','service',$2,jsonb_build_object('listing_id',$3::text,'seller',$4::text,'buyer',$5::text,'price_minor',$6::bigint))`,
		userID, result.ServiceID, listingID, seller, buyer, result.PriceMinor); err != nil {
		return TradeResult{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT s.display_name,s.billing_email,b.display_name,b.billing_email FROM accounts s, accounts b WHERE s.id=$1 AND b.id=$2`, seller, buyer).
		Scan(&result.SellerName, &result.SellerEmail, &result.BuyerName, &result.BuyerEmail); err != nil {
		return TradeResult{}, err
	}
	return result, tx.Commit(ctx)
}
