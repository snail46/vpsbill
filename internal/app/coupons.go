package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// couponAPI serves the two coupon lines: staff coupons for platform plans
// and hosts' coupons for their own plans. It also quotes a code at checkout
// and handles buyers' refunds of hosted services.
type couponAPI struct {
	coupons  *postgres.CouponStore
	settings *settings.Manager
	market   *marketplaceAPI
}

type couponRequest struct {
	postgres.CouponInput
	// ExpiresOn is an optional YYYY-MM-DD; the code works through that day
	// in the site time zone.
	ExpiresOn string `json:"expires_on"`
}

func (c *couponAPI) input(w http.ResponseWriter, r *http.Request) (postgres.CouponInput, bool) {
	var request couponRequest
	if !decodeJSON(w, r, &request) {
		return postgres.CouponInput{}, false
	}
	input := request.CouponInput
	if day := strings.TrimSpace(request.ExpiresOn); day != "" {
		parsed, err := time.ParseInLocation("2006-01-02", day, clock.Zone)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "到期日期格式无效"})
			return input, false
		}
		end := parsed.AddDate(0, 0, 1).Add(-time.Second)
		input.ExpiresAt = &end
	}
	for _, id := range input.PlanIDs {
		if !uuidPattern.MatchString(id) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "适用套餐无效"})
			return input, false
		}
	}
	if message := input.Validate(); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return input, false
	}
	return input, true
}

func writeCouponError(w http.ResponseWriter, err error) {
	var rule *postgres.HostedOrderError
	switch {
	case errors.As(err, &rule):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": rule.Message})
	case errors.Is(err, postgres.ErrCouponCodeTaken):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "code_taken", "message": "这个优惠码已被使用，请换一个"})
	case errors.Is(err, postgres.ErrCouponNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "coupon_not_found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

func (c *couponAPI) list(w http.ResponseWriter, r *http.Request, owner string) {
	rows, err := c.coupons.ListCoupons(r.Context(), owner)
	if err != nil {
		writeCouponError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (c *couponAPI) create(w http.ResponseWriter, r *http.Request, owner string) {
	input, ok := c.input(w, r)
	if !ok {
		return
	}
	id, err := c.coupons.CreateCoupon(r.Context(), owner, input)
	if err != nil {
		writeCouponError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
}

func (c *couponAPI) update(w http.ResponseWriter, r *http.Request, owner string) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "coupon_not_found"})
		return
	}
	input, ok := c.input(w, r)
	if !ok {
		return
	}
	if err := c.coupons.UpdateCoupon(r.Context(), owner, id, input); err != nil {
		writeCouponError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"id": id}})
}

func (c *couponAPI) adminList(w http.ResponseWriter, r *http.Request)   { c.list(w, r, "") }
func (c *couponAPI) adminCreate(w http.ResponseWriter, r *http.Request) { c.create(w, r, "") }
func (c *couponAPI) adminUpdate(w http.ResponseWriter, r *http.Request) { c.update(w, r, "") }

func (c *couponAPI) hostList(w http.ResponseWriter, r *http.Request) {
	c.list(w, r, customerPrincipalFromContext(r.Context()).AccountID)
}

func (c *couponAPI) hostCreate(w http.ResponseWriter, r *http.Request) {
	if c.market.marketplaceOpen(w) {
		c.create(w, r, customerPrincipalFromContext(r.Context()).AccountID)
	}
}

func (c *couponAPI) hostUpdate(w http.ResponseWriter, r *http.Request) {
	c.update(w, r, customerPrincipalFromContext(r.Context()).AccountID)
}

// quote previews a code for one plan at checkout.
func (c *couponAPI) quote(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code         string `json:"code"`
		PlanID       string `json:"plan_id"`
		BillingCycle string `json:"billing_cycle"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !uuidPattern.MatchString(input.PlanID) || strings.TrimSpace(input.Code) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请输入优惠码"})
		return
	}
	quote, err := c.coupons.Quote(r.Context(), input.Code, customerPrincipalFromContext(r.Context()).AccountID, input.PlanID, input.BillingCycle)
	if err != nil {
		writeCouponError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": quote})
}

// ---- Refunds of hosted services ----

func (c *couponAPI) refundQuote(w http.ResponseWriter, r *http.Request) {
	serviceID := r.PathValue("id")
	if !uuidPattern.MatchString(serviceID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
		return
	}
	quote, err := c.market.service.RefundQuote(r.Context(), customerPrincipalFromContext(r.Context()).AccountID, serviceID)
	if errors.Is(err, postgres.ErrServiceNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found", "message": "只有托管实例可以申请退款"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": quote})
}

func (c *couponAPI) refund(w http.ResponseWriter, r *http.Request) {
	serviceID := r.PathValue("id")
	if !uuidPattern.MatchString(serviceID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
		return
	}
	// The buyer confirms the amount they were shown; if the terms changed
	// in between (the hour passed, traffic grew) they must look again.
	var input struct {
		ExpectedMinor int64 `json:"expected_minor"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	result, err := c.market.service.Refund(r.Context(), identity.AccountID, serviceID, identity.UserID, input.ExpectedMinor)
	switch {
	case errors.Is(err, postgres.ErrServiceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
		return
	case errors.Is(err, postgres.ErrRefundUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "refund_unavailable", "message": result.Message})
		return
	case errors.Is(err, postgres.ErrRefundChanged):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "refund_changed", "message": "退款金额已变化，请重新确认", "data": result.RefundQuote})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result.RefundQuote})
}
