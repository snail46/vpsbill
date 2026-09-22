package app

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"clicd-billing/internal/config"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Dependencies struct {
	Config config.Config
	DB     *pgxpool.Pool
	Logger *slog.Logger
}

func NewHandler(deps Dependencies) (http.Handler, error) {
	secretBox, err := security.NewSecretBox(deps.Config.EncryptionKey)
	if err != nil {
		return nil, err
	}
	authStore := postgres.NewAuthStore(deps.DB)
	catalogStore := postgres.NewCatalogStore(deps.DB)
	billingStore := postgres.NewBillingStore(deps.DB)
	provisioningStore := postgres.NewProvisioningStore(deps.DB)
	portalStore := postgres.NewPortalStore(deps.DB)
	operationsStore := postgres.NewOperationsStore(deps.DB)
	monitoringStore := postgres.NewMonitoringStore(deps.DB)
	auth := newAuthenticator(deps.Config, authStore, secretBox)
	admin := newAdminCatalog(deps.Config, catalogStore, secretBox)
	billing := newAdminBilling(deps.Config, billingStore)
	automation := newAdminAutomation(provisioningStore)
	portal := newCustomerPortal(deps.Config, portalStore, billingStore, catalogStore)
	operations := newOperationsAPI(operationsStore)
	metrics := newMetricsAPI(monitoringStore, deps.Config.MetricsToken)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if err := deps.DB.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", metrics.serve)
	mux.HandleFunc("GET /api/v1/meta", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"name":        deps.Config.AppName,
				"environment": deps.Config.Environment,
				"capabilities": []string{
					"accounts", "catalog", "billing", "provisioning", "clicd", "support", "audit", "notifications",
				},
			},
		})
	})
	mux.HandleFunc("GET /api/v1/auth/bootstrap", auth.bootstrapStatus)
	mux.HandleFunc("POST /api/v1/auth/bootstrap", auth.bootstrap)
	mux.HandleFunc("POST /api/v1/auth/login", auth.login)
	mux.Handle("GET /api/v1/auth/me", auth.require("", http.HandlerFunc(auth.me)))
	mux.Handle("POST /api/v1/auth/logout", auth.require("", http.HandlerFunc(auth.logout)))
	mux.Handle("POST /api/v1/auth/mfa/setup", auth.require("", http.HandlerFunc(auth.staffMFASetup)))
	mux.Handle("POST /api/v1/auth/mfa/confirm", auth.require("", http.HandlerFunc(auth.staffMFAConfirm)))
	mux.Handle("POST /api/v1/auth/mfa/disable", auth.require("", http.HandlerFunc(auth.staffMFADisable)))
	mux.HandleFunc("POST /api/v1/customer/auth/register", auth.customerRegister)
	mux.HandleFunc("POST /api/v1/customer/auth/login", auth.customerLogin)
	mux.Handle("GET /api/v1/customer/auth/me", auth.requireCustomer(http.HandlerFunc(auth.customerMe)))
	mux.Handle("POST /api/v1/customer/auth/logout", auth.requireCustomer(http.HandlerFunc(auth.logout)))
	mux.Handle("POST /api/v1/customer/auth/mfa/setup", auth.requireCustomer(http.HandlerFunc(auth.customerMFASetup)))
	mux.Handle("POST /api/v1/customer/auth/mfa/confirm", auth.requireCustomer(http.HandlerFunc(auth.customerMFAConfirm)))
	mux.Handle("POST /api/v1/customer/auth/mfa/disable", auth.requireCustomer(http.HandlerFunc(auth.customerMFADisable)))
	mux.Handle("GET /api/v1/customer/services", auth.requireCustomer(http.HandlerFunc(portal.listServices)))
	mux.Handle("POST /api/v1/customer/services/{id}/actions/{action}", auth.requireCustomer(http.HandlerFunc(portal.serviceAction)))
	mux.Handle("GET /api/v1/customer/invoices", auth.requireCustomer(http.HandlerFunc(portal.listInvoices)))
	mux.Handle("GET /api/v1/customer/transactions", auth.requireCustomer(http.HandlerFunc(portal.listTransactions)))
	mux.Handle("GET /api/v1/customer/catalog", auth.requireCustomer(http.HandlerFunc(portal.catalogData)))
	mux.Handle("GET /api/v1/customer/orders", auth.requireCustomer(http.HandlerFunc(portal.listOrders)))
	mux.Handle("POST /api/v1/customer/orders", auth.requireCustomer(http.HandlerFunc(portal.createOrder)))
	mux.Handle("POST /api/v1/customer/invoices/{id}/checkout", auth.requireCustomer(http.HandlerFunc(portal.checkout)))
	mux.Handle("GET /api/v1/customer/tickets", auth.requireCustomer(http.HandlerFunc(operations.customerListTickets)))
	mux.Handle("POST /api/v1/customer/tickets", auth.requireCustomer(http.HandlerFunc(operations.customerCreateTicket)))
	mux.Handle("GET /api/v1/customer/tickets/{id}", auth.requireCustomer(http.HandlerFunc(operations.customerTicketDetail)))
	mux.Handle("POST /api/v1/customer/tickets/{id}/messages", auth.requireCustomer(http.HandlerFunc(operations.customerReplyTicket)))
	mux.Handle("GET /api/v1/admin/nodes", auth.require("nodes:read", http.HandlerFunc(admin.listNodes)))
	mux.Handle("POST /api/v1/admin/nodes", auth.require("nodes:write", http.HandlerFunc(admin.createNode)))
	mux.Handle("POST /api/v1/admin/nodes/{id}/test", auth.require("nodes:write", http.HandlerFunc(admin.testNode)))
	mux.Handle("GET /api/v1/admin/plans", auth.require("plans:read", http.HandlerFunc(admin.listPlans)))
	mux.Handle("POST /api/v1/admin/plans", auth.require("plans:write", http.HandlerFunc(admin.createPlan)))
	mux.Handle("PATCH /api/v1/admin/plans/{id}", auth.require("plans:write", http.HandlerFunc(admin.updatePlan)))
	mux.Handle("GET /api/v1/admin/customers", auth.require("customers:read", http.HandlerFunc(billing.listCustomers)))
	mux.Handle("POST /api/v1/admin/customers", auth.require("customers:write", http.HandlerFunc(billing.createCustomer)))
	mux.Handle("PATCH /api/v1/admin/customers/{id}", auth.require("customers:write", http.HandlerFunc(billing.updateCustomer)))
	mux.Handle("GET /api/v1/admin/regions", auth.require("nodes:read", http.HandlerFunc(billing.listRegions)))
	mux.Handle("GET /api/v1/admin/orders", auth.require("orders:read", http.HandlerFunc(billing.listOrders)))
	mux.Handle("POST /api/v1/admin/orders", auth.require("orders:write", http.HandlerFunc(billing.createOrder)))
	mux.Handle("GET /api/v1/admin/invoices", auth.require("billing:read", http.HandlerFunc(billing.listInvoices)))
	mux.Handle("GET /api/v1/admin/transactions", auth.require("billing:read", http.HandlerFunc(billing.listTransactions)))
	mux.Handle("POST /api/v1/admin/invoices/{id}/pay", auth.require("billing:write", http.HandlerFunc(billing.recordManualPayment)))
	mux.Handle("GET /api/v1/admin/services", auth.require("services:read", http.HandlerFunc(automation.listServices)))
	mux.Handle("GET /api/v1/admin/jobs", auth.require("services:read", http.HandlerFunc(automation.listJobs)))
	mux.Handle("POST /api/v1/admin/jobs/{id}/retry", auth.require("services:write", http.HandlerFunc(automation.retryJob)))
	mux.Handle("GET /api/v1/admin/tickets", auth.require("tickets:read", http.HandlerFunc(operations.adminListTickets)))
	mux.Handle("GET /api/v1/admin/tickets/{id}", auth.require("tickets:read", http.HandlerFunc(operations.adminTicketDetail)))
	mux.Handle("POST /api/v1/admin/tickets/{id}/messages", auth.require("tickets:write", http.HandlerFunc(operations.adminReplyTicket)))
	mux.Handle("PATCH /api/v1/admin/tickets/{id}", auth.require("tickets:write", http.HandlerFunc(operations.adminUpdateTicket)))
	mux.Handle("GET /api/v1/admin/audit-logs", auth.require("audit:read", http.HandlerFunc(operations.adminAuditLogs)))
	mux.HandleFunc("POST /api/v1/webhooks/payments/generic", billing.paymentWebhook)

	return requestLog(deps.Logger, securityHeaders(mux)), nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "请求内容无效"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "请求只能包含一个 JSON 对象"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}
