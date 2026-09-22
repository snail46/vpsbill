package app

import (
	"errors"
	"net/http"

	"clicd-billing/internal/payment"
	"clicd-billing/internal/settings"
	"clicd-billing/internal/store/postgres"
)

type customerPortal struct {
	store    *postgres.PortalStore
	billing  *postgres.BillingStore
	catalog  *postgres.CatalogStore
	settings *settings.Manager
}

func newCustomerPortal(runtime *settings.Manager, store *postgres.PortalStore, billing *postgres.BillingStore, catalog *postgres.CatalogStore) *customerPortal {
	return &customerPortal{store: store, billing: billing, catalog: catalog, settings: runtime}
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
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"plans": available, "regions": regions, "checkout_enabled": p.settings.Current().PaymentGateway.Type != "disabled"}})
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
	runtime := p.settings.Current()
	config := runtime.PaymentGateway
	if config.Type == "" || config.Type == "disabled" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "checkout_unavailable", "message": "商家尚未配置在线支付网关"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	intent, err := p.billing.PreparePaymentIntent(r.Context(), identity.AccountID, r.PathValue("id"), config.Type)
	if errors.Is(err, postgres.ErrInvoiceUnavailable) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "invoice_unavailable", "message": "账单不存在、已支付或当前不可付款"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if (config.Type == "alipay_f2f" || config.Type == "epay") && intent.Currency != "CNY" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "currency_unsupported", "message": "当前支付网关仅支持人民币账单"})
		return
	}
	if intent.CheckoutURL == "" {
		request := payment.CheckoutRequest{MerchantReference: intent.MerchantReference, InvoiceNumber: intent.InvoiceNumber, AmountMinor: intent.AmountMinor, Currency: intent.Currency, ExpiresAt: intent.ExpiresAt}
		switch config.Type {
		case "generic":
			intent.CheckoutURL, err = (payment.GenericGateway{BaseURL: config.GenericBaseURL, PublicURL: runtime.PublicURL, Secret: config.GenericSecret}).CheckoutURL(request)
		case "epay":
			intent.CheckoutURL, err = (payment.EpayGateway{APIURL: config.EpayAPIURL, PartnerID: config.EpayPartnerID, MerchantKey: config.EpayMerchantKey, PaymentType: config.EpayPaymentType, PublicURL: runtime.PublicURL, SiteName: runtime.AppName}).CheckoutURL(request)
		case "alipay_f2f":
			intent.CheckoutURL, err = (payment.AlipayGateway{AppID: config.AlipayAppID, PrivateKey: config.AlipayPrivateKey, PublicKey: config.AlipayPublicKey, GatewayURL: config.AlipayGatewayURL, PublicURL: runtime.PublicURL}).CheckoutURL(r.Context(), request)
		default:
			err = errors.New("unsupported payment gateway")
		}
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
