package app

import (
	"errors"
	"net/http"

	"clicd-billing/internal/config"
	"clicd-billing/internal/payment"
	"clicd-billing/internal/store/postgres"
)

type customerPortal struct {
	store        *postgres.PortalStore
	billing      *postgres.BillingStore
	catalog      *postgres.CatalogStore
	gateway      payment.GenericGateway
	providerName string
}

func newCustomerPortal(cfg config.Config, store *postgres.PortalStore, billing *postgres.BillingStore, catalog *postgres.CatalogStore) *customerPortal {
	return &customerPortal{store: store, billing: billing, catalog: catalog, gateway: payment.GenericGateway{BaseURL: cfg.PaymentCheckoutURL, PublicURL: cfg.PublicURL, Secret: cfg.PaymentWebhookSecret}, providerName: cfg.PaymentProviderName}
}

func (p *customerPortal) listServices(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.store.ListServices(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (p *customerPortal) listInvoices(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.store.ListInvoices(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (p *customerPortal) listTransactions(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.store.ListTransactions(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (p *customerPortal) serviceAction(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	jobID, err := p.store.QueueServiceAction(r.Context(), identity.AccountID, identity.UserID, r.PathValue("id"), r.PathValue("action"), remoteIP(r), r.UserAgent())
	switch {
	case errors.Is(err, postgres.ErrServiceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
	case errors.Is(err, postgres.ErrServiceActionUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_unavailable", "message": "当前服务或运行状态不允许该操作"})
	case errors.Is(err, postgres.ErrActionInProgress):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_in_progress", "message": "相同操作正在处理中"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"job_id": jobID, "status": "pending"}})
	}
}

func (p *customerPortal) catalogData(w http.ResponseWriter, r *http.Request) {
	plans, err := p.catalog.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	available := make([]postgres.Plan, 0)
	for _, plan := range plans {
		if plan.Enabled && len(plan.Prices) > 0 {
			available = append(available, plan)
		}
	}
	regions, err := p.billing.ListRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"plans": available, "regions": regions, "checkout_enabled": p.gateway.Available()}})
}

func (p *customerPortal) listOrders(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.billing.ListOrdersByAccount(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (p *customerPortal) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Items []postgres.OrderItemInput `json:"items"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	order, err := p.billing.CreateOrder(r.Context(), postgres.CreateOrderInput{AccountID: identity.AccountID, Items: input.Items, ActorType: "customer", ActorID: identity.UserID})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "order_create_failed", "message": "地区、套餐、价格或配置无效"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": order})
}

func (p *customerPortal) checkout(w http.ResponseWriter, r *http.Request) {
	if !p.gateway.Available() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "checkout_unavailable", "message": "商家尚未配置在线支付网关"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	intent, err := p.billing.PreparePaymentIntent(r.Context(), identity.AccountID, r.PathValue("id"), p.providerName)
	if errors.Is(err, postgres.ErrInvoiceUnavailable) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "invoice_unavailable", "message": "账单不存在、已支付或当前不可付款"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if intent.CheckoutURL == "" {
		intent.CheckoutURL, err = p.gateway.CheckoutURL(payment.CheckoutRequest{MerchantReference: intent.MerchantReference, InvoiceNumber: intent.InvoiceNumber, AmountMinor: intent.AmountMinor, Currency: intent.Currency, ExpiresAt: intent.ExpiresAt})
		if err == nil {
			err = p.billing.SetPaymentIntentCheckoutURL(r.Context(), intent.ID, identity.AccountID, intent.CheckoutURL)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "checkout_create_failed"})
			return
		}
		intent.Status = "redirected"
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": intent})
}
