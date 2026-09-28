package app

import (
	"context"
	"errors"
	"net/http"

	"vpsbill/internal/notify"
	"vpsbill/internal/store/postgres"
)

// tradeAPI serves the trading market, where customers resell running
// instances to each other for balance.
type tradeAPI struct {
	store    *postgres.TradeStore
	notifier *notify.Notifier
}

func writeTradeError(w http.ResponseWriter, err error) {
	var rule *postgres.TradeError
	switch {
	case errors.As(err, &rule):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "trade_unavailable", "message": rule.Message})
	case errors.Is(err, postgres.ErrListingNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "listing_not_found", "message": "挂售不存在或已下架"})
	case errors.Is(err, postgres.ErrServiceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
	case errors.Is(err, postgres.ErrListingChanged):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "listing_changed", "message": "价格已变化，请刷新后重新确认"})
	case errors.Is(err, postgres.ErrInsufficientBalance):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "insufficient_balance", "message": "余额不足，请先充值"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

func (t *tradeAPI) market(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	market, err := t.store.Market(r.Context(), identity.AccountID)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	mine, err := t.store.SellerListings(r.Context(), identity.AccountID)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"listings": market, "mine": mine, "hold_days": postgres.TradeHoldDays}})
}

func (t *tradeAPI) createListing(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ServiceID  string `json:"service_id"`
		PriceMinor int64  `json:"price_minor"`
		Note       string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !uuidPattern.MatchString(input.ServiceID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	id, err := t.store.CreateListing(r.Context(), identity.AccountID, identity.UserID, input.ServiceID, input.PriceMinor, input.Note)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
}

func (t *tradeAPI) cancelListing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		writeTradeError(w, postgres.ErrListingNotFound)
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	if err := t.store.CancelListing(r.Context(), identity.AccountID, "customer", identity.UserID, id, "卖家下架"); err != nil {
		writeTradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"id": id}})
}

func (t *tradeAPI) buy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		writeTradeError(w, postgres.ErrListingNotFound)
		return
	}
	var input struct {
		ExpectedMinor int64 `json:"expected_minor"`
		AcceptRisk    bool  `json:"accept_risk"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.AcceptRisk {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "risk_not_accepted", "message": "请先确认已了解交易风险"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	result, err := t.store.Buy(r.Context(), identity.AccountID, identity.UserID, id, input.ExpectedMinor)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	if t.notifier != nil {
		t.notifier.TradeCompleted(context.WithoutCancel(r.Context()), result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"service_id": result.ServiceID, "instance_name": result.InstanceName}})
}

func (t *tradeAPI) adminListings(w http.ResponseWriter, r *http.Request) {
	rows, err := t.store.AllListings(r.Context())
	if err != nil {
		writeTradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (t *tradeAPI) adminCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input struct {
		Reason string `json:"reason"`
	}
	if !uuidPattern.MatchString(id) {
		writeTradeError(w, postgres.ErrListingNotFound)
		return
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	reason := input.Reason
	if reason == "" {
		reason = "管理员下架"
	}
	if err := t.store.CancelListing(r.Context(), "", "staff", principalFromContext(r.Context()).UserID, id, reason); err != nil {
		writeTradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"id": id}})
}
