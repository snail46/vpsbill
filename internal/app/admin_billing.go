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
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"vpsbill/internal/payment"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

type adminBilling struct {
	settings *settings.Manager
	store    *postgres.BillingStore
}

func newAdminBilling(runtime *settings.Manager, store *postgres.BillingStore) *adminBilling {
	return &adminBilling{settings: runtime, store: store}
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
	runtime := a.settings.Current()
	config := runtime.PaymentGateway
	if config.Type != "generic" || !validPaymentSignature(body, r.Header.Get("X-Payment-Signature"), config.GenericSecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_signature"})
		return
	}
	var event postgres.PaymentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_event"})
		return
	}
	event.Provider = "generic"
	event.Payload = append([]byte(nil), body...)
	if event.ProviderEventID == "" || event.ProviderTransactionID == "" || event.InvoiceNumber == "" || event.EventType != "payment.succeeded" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "unsupported_event"})
		return
	}
	result, err := a.store.ProcessPayment(r.Context(), event, "payment_provider", "generic")
	if err != nil {
		writePaymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (a *adminBilling) epayWebhook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		plainWebhook(w, "fail")
		return
	}
	config := a.settings.Current().PaymentGateway
	if config.Type != "epay" || r.Form.Get("pid") != config.EpayPartnerID || !payment.VerifyEpay(r.Form, config.EpayMerchantKey) || r.Form.Get("trade_status") != "TRADE_SUCCESS" {
		plainWebhook(w, "fail")
		return
	}
	intent, err := a.store.PaymentIntentByMerchantReference(r.Context(), r.Form.Get("out_trade_no"), "epay")
	amount, amountErr := parseAmountMinor(r.Form.Get("money"))
	if err != nil || amountErr != nil || amount != intent.AmountMinor || intent.Currency != "CNY" || r.Form.Get("trade_no") == "" {
		plainWebhook(w, "fail")
		return
	}
	payload, _ := json.Marshal(r.Form)
	_, err = a.store.ProcessPayment(r.Context(), postgres.PaymentEvent{Provider: "epay", ProviderEventID: r.Form.Get("trade_no"), EventType: "payment.succeeded", ProviderTransactionID: r.Form.Get("trade_no"), InvoiceNumber: intent.InvoiceNumber, AmountMinor: amount, Currency: intent.Currency, Payload: payload}, "payment_provider", "epay")
	if err != nil {
		plainWebhook(w, "fail")
		return
	}
	plainWebhook(w, "success")
}

func (a *adminBilling) alipayWebhook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		plainWebhook(w, "failure")
		return
	}
	config := a.settings.Current().PaymentGateway
	status := r.Form.Get("trade_status")
	if config.Type != "alipay_f2f" || r.Form.Get("app_id") != config.AlipayAppID || (status != "TRADE_SUCCESS" && status != "TRADE_FINISHED") || !payment.VerifyAlipay(r.Form, config.AlipayPublicKey) {
		plainWebhook(w, "failure")
		return
	}
	intent, err := a.store.PaymentIntentByMerchantReference(r.Context(), r.Form.Get("out_trade_no"), "alipay_f2f")
	amount, amountErr := parseAmountMinor(r.Form.Get("total_amount"))
	if err != nil || amountErr != nil || amount != intent.AmountMinor || intent.Currency != "CNY" || r.Form.Get("trade_no") == "" || r.Form.Get("notify_id") == "" {
		plainWebhook(w, "failure")
		return
	}
	payload, _ := json.Marshal(r.Form)
	_, err = a.store.ProcessPayment(r.Context(), postgres.PaymentEvent{Provider: "alipay_f2f", ProviderEventID: r.Form.Get("notify_id"), EventType: "payment.succeeded", ProviderTransactionID: r.Form.Get("trade_no"), InvoiceNumber: intent.InvoiceNumber, AmountMinor: amount, Currency: intent.Currency, Payload: payload}, "payment_provider", "alipay_f2f")
	if err != nil {
		plainWebhook(w, "failure")
		return
	}
	plainWebhook(w, "success")
}

func parseAmountMinor(value string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("invalid amount")
	}
	major, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || major < 0 {
		return 0, errors.New("invalid amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return 0, errors.New("invalid amount")
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	minor := int64(0)
	if fraction != "" {
		minor, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("invalid amount")
		}
	}
	return major*100 + minor, nil
}

func plainWebhook(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(value))
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
