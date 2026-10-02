package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCouponNotFound  = errors.New("coupon not found")
	ErrCouponCodeTaken = errors.New("coupon code is already used")
)

var couponCodePattern = regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`)

// Coupon is a discount code. Platform coupons have no owner and apply to
// platform plans; a host's coupons apply to that host's plans.
type Coupon struct {
	ID             string   `json:"id"`
	Code           string   `json:"code"`
	OwnerAccountID string   `json:"-"`
	Description    string   `json:"description"`
	DiscountType   string   `json:"discount_type"`
	DiscountValue  int64    `json:"discount_value"`
	PlanIDs        []string `json:"plan_ids"`
	MaxUses        int      `json:"max_uses"`
	// PerAccountLimit caps the discounted instances one account gets; 0
	// means no limit.
	PerAccountLimit int        `json:"per_account_limit"`
	UsedCount       int        `json:"used_count"`
	ExpiresAt       *time.Time `json:"expires_at"`
	Recurring       bool       `json:"recurring"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"created_at"`
}

type CouponInput struct {
	Code            string     `json:"code"`
	Description     string     `json:"description"`
	DiscountType    string     `json:"discount_type"`
	DiscountValue   int64      `json:"discount_value"`
	PlanIDs         []string   `json:"plan_ids"`
	MaxUses         int        `json:"max_uses"`
	PerAccountLimit int        `json:"per_account_limit"`
	ExpiresAt       *time.Time `json:"-"`
	Recurring       bool       `json:"recurring"`
	Enabled         bool       `json:"enabled"`
}

// Validate returns a user-facing message, or "" when the input is fine.
func (in *CouponInput) Validate() string {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case !couponCodePattern.MatchString(in.Code):
		return "优惠码需为 3-32 位字母、数字、下划线或短横线"
	case len([]rune(in.Description)) > 200:
		return "说明不能超过 200 个字符"
	case in.DiscountType == "percent" && (in.DiscountValue < 1 || in.DiscountValue > 99):
		return "折扣比例需在 1%-99% 之间"
	case in.DiscountType == "amount" && (in.DiscountValue < 1 || in.DiscountValue > 10_000_000):
		return "减免金额需在 ¥0.01 到 ¥100000 之间"
	case in.DiscountType != "percent" && in.DiscountType != "amount":
		return "请选择折扣方式"
	case in.MaxUses < 0 || in.MaxUses > 1_000_000:
		return "最大使用次数无效"
	case in.PerAccountLimit < 0 || in.PerAccountLimit > 10_000:
		return "每账号限用次数需在 0–10000 之间"
	case in.MaxUses > 0 && in.PerAccountLimit > in.MaxUses:
		return "每账号限用次数不能超过总使用次数"
	case len(in.PlanIDs) > 100:
		return "适用套餐过多"
	}
	return ""
}

// CouponDiscount is the discount on one unit priced at unit. The buyer
// always pays at least 0.01, since invoices must have something to pay.
func CouponDiscount(kind string, value, unit int64) int64 {
	var discount int64
	switch kind {
	case "percent":
		discount = int64(math.Round(float64(unit) * float64(value) / 100))
	case "amount":
		discount = value
	}
	if discount > unit-1 {
		discount = unit - 1
	}
	if discount < 0 {
		return 0
	}
	return discount
}

type CouponStore struct{ db *pgxpool.Pool }

func NewCouponStore(db *pgxpool.Pool) *CouponStore { return &CouponStore{db: db} }

// couponUsesSQL counts discounted units in paid orders and in unpaid orders
// that can still be paid.
const couponUsesSQL = `coalesce((SELECT sum(r.units) FROM coupon_redemptions r JOIN invoices i ON i.id=r.invoice_id
	WHERE r.coupon_id=c.id AND (r.status='paid' OR (i.status='open' AND i.due_at>now()))),0)::int`

// couponAccountUsesSQL counts the same for one account ($2).
const couponAccountUsesSQL = `coalesce((SELECT sum(r.units) FROM coupon_redemptions r JOIN invoices i ON i.id=r.invoice_id
	WHERE r.coupon_id=c.id AND r.account_id=$2 AND (r.status='paid' OR (i.status='open' AND i.due_at>now()))),0)::int`

// ListCoupons returns an owner's coupons; an empty owner lists platform coupons.
func (s *CouponStore) ListCoupons(ctx context.Context, ownerID string) ([]Coupon, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id,c.code,coalesce(c.owner_account_id::text,''),c.description,c.discount_type,c.discount_value,c.plan_ids::text[],
		       c.max_uses,c.per_account_limit,`+couponUsesSQL+`,c.expires_at,c.recurring,c.enabled,c.created_at
		FROM coupons c WHERE c.owner_account_id IS NOT DISTINCT FROM nullif($1,'')::uuid ORDER BY c.created_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Coupon, 0)
	for rows.Next() {
		var c Coupon
		if err := rows.Scan(&c.ID, &c.Code, &c.OwnerAccountID, &c.Description, &c.DiscountType, &c.DiscountValue, &c.PlanIDs,
			&c.MaxUses, &c.PerAccountLimit, &c.UsedCount, &c.ExpiresAt, &c.Recurring, &c.Enabled, &c.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// checkCouponPlans rejects plans the owner does not sell.
func checkCouponPlans(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, ownerID string, planIDs []string) error {
	if len(planIDs) == 0 {
		return nil
	}
	var matched int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM plans WHERE id::text=ANY($1) AND owner_account_id IS NOT DISTINCT FROM nullif($2,'')::uuid`, planIDs, ownerID).Scan(&matched); err != nil {
		return err
	}
	if matched != len(planIDs) {
		return &HostedOrderError{"适用套餐无效"}
	}
	return nil
}

func (s *CouponStore) CreateCoupon(ctx context.Context, ownerID string, in CouponInput) (string, error) {
	if err := checkCouponPlans(ctx, s.db, ownerID, in.PlanIDs); err != nil {
		return "", err
	}
	var id string
	err := s.db.QueryRow(ctx, `
		INSERT INTO coupons(code,owner_account_id,description,discount_type,discount_value,plan_ids,max_uses,expires_at,recurring,enabled,per_account_limit)
		VALUES($1,nullif($2,'')::uuid,$3,$4,$5,$6::uuid[],$7,$8,$9,$10,$11) RETURNING id
	`, in.Code, ownerID, in.Description, in.DiscountType, in.DiscountValue, nonNilStrings(in.PlanIDs), in.MaxUses, in.ExpiresAt, in.Recurring, in.Enabled, in.PerAccountLimit).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", ErrCouponCodeTaken
	}
	return id, err
}

// UpdateCoupon changes everything but the code. Instances already bought
// keep the renewal discount they were sold with.
func (s *CouponStore) UpdateCoupon(ctx context.Context, ownerID, id string, in CouponInput) error {
	if err := checkCouponPlans(ctx, s.db, ownerID, in.PlanIDs); err != nil {
		return err
	}
	command, err := s.db.Exec(ctx, `
		UPDATE coupons SET description=$3,discount_type=$4,discount_value=$5,plan_ids=$6::uuid[],max_uses=$7,expires_at=$8,recurring=$9,enabled=$10,per_account_limit=$11,updated_at=now()
		WHERE id=$1 AND owner_account_id IS NOT DISTINCT FROM nullif($2,'')::uuid
	`, id, ownerID, in.Description, in.DiscountType, in.DiscountValue, nonNilStrings(in.PlanIDs), in.MaxUses, in.ExpiresAt, in.Recurring, in.Enabled, in.PerAccountLimit)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrCouponNotFound
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// couponLine is one order item's share of a coupon.
type couponLine struct {
	DiscountUnit  int64
	RenewalType   string
	RenewalValue  int64
	EligibleUnits int
}

type appliedCoupon struct {
	ID          string
	Code        string
	Lines       []couponLine
	Units       int
	TotalMinor  int64
	Description string
}

type couponItem struct {
	PlanID   string
	Unit     int64
	Quantity int
}

// applyCoupon checks a code against the items being bought and works out
// the discount per item. The coupon row is locked so concurrent orders
// cannot use more than max_uses.
func applyCoupon(ctx context.Context, tx pgx.Tx, code, buyerID string, items []couponItem) (appliedCoupon, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	var c Coupon
	// accountUses is what this buyer already got from the coupon; the row
	// lock below also serializes it.
	var accountUses int
	err := tx.QueryRow(ctx, `
		SELECT c.id,c.code,coalesce(c.owner_account_id::text,''),c.description,c.discount_type,c.discount_value,c.plan_ids::text[],c.max_uses,`+couponUsesSQL+`,c.expires_at,c.recurring,c.enabled,
		       c.per_account_limit,`+couponAccountUsesSQL+`
		FROM coupons c WHERE c.code=$1 FOR UPDATE OF c
	`, code, buyerID).Scan(&c.ID, &c.Code, &c.OwnerAccountID, &c.Description, &c.DiscountType, &c.DiscountValue, &c.PlanIDs, &c.MaxUses, &c.UsedCount, &c.ExpiresAt, &c.Recurring, &c.Enabled,
		&c.PerAccountLimit, &accountUses)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !c.Enabled {
		return appliedCoupon{}, &HostedOrderError{"优惠码无效"}
	}
	if err != nil {
		return appliedCoupon{}, err
	}
	if c.ExpiresAt != nil && !c.ExpiresAt.After(time.Now()) {
		return appliedCoupon{}, &HostedOrderError{"优惠码已过期"}
	}
	if c.OwnerAccountID != "" && c.OwnerAccountID == buyerID {
		return appliedCoupon{}, &HostedOrderError{"不能使用自己发布的优惠码"}
	}
	result := appliedCoupon{ID: c.ID, Code: c.Code, Description: c.Description, Lines: make([]couponLine, len(items))}
	for i, item := range items {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT coalesce(owner_account_id::text,'') FROM plans WHERE id=$1`, item.PlanID).Scan(&owner); err != nil {
			return appliedCoupon{}, err
		}
		if owner != c.OwnerAccountID || len(c.PlanIDs) > 0 && !stringAllowed(item.PlanID, c.PlanIDs) {
			continue
		}
		discount := CouponDiscount(c.DiscountType, c.DiscountValue, item.Unit)
		if discount <= 0 {
			continue
		}
		line := couponLine{DiscountUnit: discount, EligibleUnits: item.Quantity}
		if c.Recurring {
			line.RenewalType, line.RenewalValue = c.DiscountType, c.DiscountValue
		}
		result.Lines[i] = line
		result.Units += item.Quantity
		result.TotalMinor += discount * int64(item.Quantity)
	}
	if result.Units == 0 {
		return appliedCoupon{}, &HostedOrderError{"优惠码不适用于所选套餐"}
	}
	if c.MaxUses > 0 && c.UsedCount+result.Units > c.MaxUses {
		if c.UsedCount >= c.MaxUses {
			return appliedCoupon{}, &HostedOrderError{"优惠码已达到使用次数上限"}
		}
		return appliedCoupon{}, &HostedOrderError{fmt.Sprintf("优惠码只剩 %d 次可用", c.MaxUses-c.UsedCount)}
	}
	if c.PerAccountLimit > 0 && accountUses+result.Units > c.PerAccountLimit {
		if accountUses >= c.PerAccountLimit {
			return appliedCoupon{}, &HostedOrderError{fmt.Sprintf("该优惠码每个账号限用 %d 次，你已用完", c.PerAccountLimit)}
		}
		return appliedCoupon{}, &HostedOrderError{fmt.Sprintf("该优惠码每个账号限用 %d 次，你还能用 %d 次", c.PerAccountLimit, c.PerAccountLimit-accountUses)}
	}
	return result, nil
}

// CouponQuote previews a coupon for one plan before ordering.
type CouponQuote struct {
	Code          string `json:"code"`
	Description   string `json:"description"`
	UnitMinor     int64  `json:"unit_minor"`
	DiscountMinor int64  `json:"discount_minor"`
	FinalMinor    int64  `json:"final_minor"`
	Recurring     bool   `json:"recurring"`
}

func (s *CouponStore) Quote(ctx context.Context, code, buyerID, planID, cycle string) (CouponQuote, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return CouponQuote{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var unit int64
	err = tx.QueryRow(ctx, `
		SELECT pp.amount_minor FROM plan_prices pp JOIN accounts a ON a.id=$2
		WHERE pp.plan_id=$1 AND pp.currency=a.default_currency AND pp.billing_cycle=$3
		  AND pp.active_from<=now() AND (pp.active_until IS NULL OR pp.active_until>now())
		ORDER BY pp.active_from DESC LIMIT 1
	`, planID, buyerID, cycle).Scan(&unit)
	if errors.Is(err, pgx.ErrNoRows) {
		return CouponQuote{}, &HostedOrderError{"套餐价格不可用"}
	}
	if err != nil {
		return CouponQuote{}, err
	}
	applied, err := applyCoupon(ctx, tx, code, buyerID, []couponItem{{PlanID: planID, Unit: unit, Quantity: 1}})
	if err != nil {
		return CouponQuote{}, err
	}
	line := applied.Lines[0]
	return CouponQuote{Code: applied.Code, Description: applied.Description, UnitMinor: unit, DiscountMinor: line.DiscountUnit,
		FinalMinor: unit - line.DiscountUnit, Recurring: line.RenewalType != ""}, nil
}
