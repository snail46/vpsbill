package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"clicd-billing/internal/config"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"
	"github.com/jackc/pgx/v5"
)

type adminBilling struct {
	cfg   config.Config
	store *postgres.BillingStore
}

func newAdminBilling(cfg config.Config, store *postgres.BillingStore) *adminBilling {
	return &adminBilling{cfg: cfg, store: store}
}

func (a *adminBilling) listCustomers(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.store.ListAccounts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": accounts})
}

func (a *adminBilling) createCustomer(w http.ResponseWriter, r *http.Request) {
	var input postgres.Account
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Kind != "individual" && input.Kind != "business" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "客户类型必须为个人或企业"})
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" || !validEmail(input.BillingEmail) || len(strings.TrimSpace(input.DefaultCurrency)) != 3 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "客户名称、账单邮箱或币种无效"})
		return
	}
	if input.CountryCode != "" && len(strings.TrimSpace(input.CountryCode)) != 2 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "国家代码必须为两位字符"})
		return
	}
	account, err := a.store.CreateAccount(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "customer_create_failed", "message": "无法创建客户"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": account})
}

func (a *adminBilling) updateCustomer(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := principalFromContext(r.Context())
	err := a.store.UpdateAccountStatus(r.Context(), r.PathValue("id"), input.Status, principal.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "customer_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "customer_update_failed", "message": "客户状态无效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"status": input.Status}})
}

func (a *adminBilling) listRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := a.store.ListRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": regions})
}

func (a *adminBilling) listOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := a.store.ListOrders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": orders})
}

func (a *adminBilling) createOrder(w http.ResponseWriter, r *http.Request) {
	var input postgres.CreateOrderInput
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := principalFromContext(r.Context())
	input.ActorType = "staff"
	input.ActorID = principal.UserID
	order, err := a.store.CreateOrder(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "order_create_failed", "message": "客户、地区、套餐、价格或订单参数无效"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": order})
}

func (a *adminBilling) listInvoices(w http.ResponseWriter, r *http.Request) {
	invoices, err := a.store.ListInvoices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": invoices})
}

func (a *adminBilling) listTransactions(w http.ResponseWriter, r *http.Request) {
	transactions, err := a.store.ListTransactions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": transactions})
}

func (a *adminBilling) recordManualPayment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reference string `json:"reference"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	invoice, err := a.store.InvoiceByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "invoice_not_found"})
		return
	}
	eventID, _, err := security.NewToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	reference := strings.TrimSpace(input.Reference)
	if reference == "" {
		reference = "MANUAL-" + eventID[:12]
	}
	payload, _ := json.Marshal(map[string]any{"source": "admin", "reference": reference})
	principal := principalFromContext(r.Context())
	result, err := a.store.ProcessPayment(r.Context(), postgres.PaymentEvent{
		Provider:              "manual",
		ProviderEventID:       eventID,
		EventType:             "payment.succeeded",
		ProviderTransactionID: reference,
		InvoiceNumber:         invoice.Number,
		AmountMinor:           invoice.BalanceMinor,
		Currency:              invoice.Currency,
		Payload:               payload,
	}, "staff", principal.UserID)
	if err != nil {
		writePaymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (a *adminBilling) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	if !validPaymentSignature(body, r.Header.Get("X-Payment-Signature"), a.cfg.PaymentWebhookSecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_signature"})
		return
	}
	var event postgres.PaymentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_event"})
		return
	}
	event.Provider = a.cfg.PaymentProviderName
	event.Payload = append([]byte(nil), body...)
	if event.ProviderEventID == "" || event.ProviderTransactionID == "" || event.InvoiceNumber == "" || event.EventType != "payment.succeeded" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "unsupported_event"})
		return
	}
	result, err := a.store.ProcessPayment(r.Context(), event, "payment_provider", a.cfg.PaymentProviderName)
	if err != nil {
		writePaymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func validPaymentSignature(body []byte, signature, secret string) bool {
	supplied := strings.TrimPrefix(strings.TrimSpace(signature), "sha256=")
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write(body)
	expected := hex.EncodeToString(expectedMAC.Sum(nil))
	return supplied != "" && subtle.ConstantTimeCompare([]byte(strings.ToLower(supplied)), []byte(expected)) == 1
}

func writePaymentError(w http.ResponseWriter, err error) {
	if errors.Is(err, postgres.ErrPaymentMismatch) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "payment_mismatch", "message": "支付金额、币种或账单状态不匹配"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "payment_processing_failed"})
}
