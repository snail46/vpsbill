package app

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/chat"
	"vpsbill/internal/config"
	"vpsbill/internal/marketplace"
	"vpsbill/internal/notify"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Dependencies struct {
	Config   config.Config
	DB       *pgxpool.Pool
	Logger   *slog.Logger
	Settings *settings.Manager
	// AgentGateway serves Hatch agent connections; nil disables the endpoint.
	AgentGateway http.Handler
	// AgentInternal serves agent requests forwarded by other API instances.
	AgentInternal http.Handler
	// Notifier queues notification mail; nil builds one from DB and Settings.
	Notifier *notify.Notifier
	// Marketplace settles hosted-node clearances; nil builds one.
	Marketplace *marketplace.Service
	// ChatHub pushes hosted-node chat messages; nil disables live updates
	// (messages still load over HTTP).
	ChatHub *chat.Hub
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
	if deps.Settings == nil {
		return nil, errors.New("settings manager is required")
	}
	auth := newAuthenticator(deps.Config, authStore, secretBox, deps.Settings)
	admin := newAdminCatalog(catalogStore, secretBox, deps.Settings)
	billing := newAdminBilling(deps.Settings, billingStore)
	adminSettings := adminSettings{settings: deps.Settings}
	automation := newAdminAutomation(provisioningStore)
	portal := newCustomerPortal(deps.Settings, portalStore, billingStore, catalogStore, secretBox, newConsoleTickets(deps.Config.SessionSecret+deps.Config.EncryptionKey))
	notifier := deps.Notifier
	if notifier == nil {
		notifier = notify.New(postgres.NewMailStore(deps.DB), deps.Settings, secretBox, deps.Logger)
	}
	operations := newOperationsAPI(operationsStore, deps.Settings, notifier)
	metrics := newMetricsAPI(monitoringStore, deps.Settings)
	marketStore := postgres.NewMarketplaceStore(deps.DB)
	marketService := deps.Marketplace
	if marketService == nil {
		marketService = marketplace.New(marketStore, catalogStore, secretBox, deps.Settings, notifier, deps.Logger)
	}
	market := &marketplaceAPI{store: marketStore, catalog: catalogStore, billing: billingStore, settings: deps.Settings, box: secretBox, service: marketService, hub: deps.ChatHub}
	coupons := &couponAPI{coupons: postgres.NewCouponStore(deps.DB), settings: deps.Settings, market: market}
	overview := &overviewAPI{announcements: postgres.NewAnnouncementStore(deps.DB), billing: billingStore, market: marketStore}
	trade := &tradeAPI{store: postgres.NewTradeStore(deps.DB), notifier: notifier, hosting: marketService, settings: deps.Settings}
	install := &installer{settings: deps.Settings, auth: auth, logger: deps.Logger}

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
	mux.HandleFunc("GET /api/v1/meta", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"surface":                  r.Header.Get("X-VPSBill-Surface"),
				"admin_url":                deps.Settings.Current().AdminURL,
				"public_url":               deps.Settings.Current().PublicURL,
				"name":                     deps.Settings.Current().AppName,
				"environment":              deps.Config.Environment,
				"installed":                deps.Settings.Current().Installed,
				"password_reset_mail":      deps.Settings.Current().SMTP.Configured(),
				"ticket_attachment_max_mb": deps.Settings.Current().TicketAttachmentMaxMB,
				"marketplace_enabled":      deps.Settings.Current().Marketplace.Enabled,
				"capabilities": []string{
					"accounts", "catalog", "billing", "provisioning", "clicd", "support", "audit", "notifications", "wallet", "marketplace",
				},
			},
		})
	})
	mux.HandleFunc("GET /api/v1/install", install.status)
	mux.HandleFunc("POST /api/v1/install", install.install)
	mux.HandleFunc("POST /api/v1/auth/login", auth.login)
	mux.Handle("GET /api/v1/auth/me", auth.require("", http.HandlerFunc(auth.me)))
	mux.Handle("POST /api/v1/auth/logout", auth.require("", http.HandlerFunc(auth.logout)))
	mux.Handle("POST /api/v1/auth/mfa/setup", auth.require("", http.HandlerFunc(auth.staffMFASetup)))
	mux.Handle("POST /api/v1/auth/mfa/confirm", auth.require("", http.HandlerFunc(auth.staffMFAConfirm)))
	mux.Handle("POST /api/v1/auth/mfa/disable", auth.require("", http.HandlerFunc(auth.staffMFADisable)))
	mux.Handle("POST /api/v1/auth/password", auth.require("", http.HandlerFunc(auth.staffChangePassword)))
	mux.HandleFunc("POST /api/v1/customer/auth/register", auth.customerRegister)
	mux.HandleFunc("POST /api/v1/customer/auth/login", auth.customerLogin)
	mux.HandleFunc("POST /api/v1/customer/auth/password-reset", auth.customerRequestPasswordReset)
	mux.HandleFunc("POST /api/v1/customer/auth/verify-email", auth.customerConfirmEmail)
	mux.Handle("POST /api/v1/customer/auth/verify-email/resend", auth.requireCustomer(http.HandlerFunc(auth.customerResendVerification)))
	mux.HandleFunc("POST /api/v1/customer/auth/password-reset/confirm", auth.customerResetPassword)
	mux.Handle("GET /api/v1/customer/auth/me", auth.requireCustomer(http.HandlerFunc(auth.customerMe)))
	mux.Handle("POST /api/v1/customer/auth/logout", auth.requireCustomer(http.HandlerFunc(auth.customerLogout)))
	mux.Handle("POST /api/v1/customer/auth/mfa/setup", auth.requireCustomer(http.HandlerFunc(auth.customerMFASetup)))
	mux.Handle("POST /api/v1/customer/auth/mfa/confirm", auth.requireCustomer(http.HandlerFunc(auth.customerMFAConfirm)))
	mux.Handle("POST /api/v1/customer/auth/mfa/disable", auth.requireCustomer(http.HandlerFunc(auth.customerMFADisable)))
	mux.Handle("POST /api/v1/customer/auth/password", auth.requireCustomer(http.HandlerFunc(auth.customerChangePassword)))
	mux.Handle("GET /api/v1/customer/services", auth.requireCustomer(http.HandlerFunc(portal.listServices)))
	mux.Handle("PUT /api/v1/customer/services/{id}/auto-renew", auth.requireCustomer(http.HandlerFunc(portal.setAutoRenew)))
	mux.Handle("GET /api/v1/customer/overview", auth.requireCustomer(http.HandlerFunc(overview.customerOverview)))
	mux.Handle("GET /api/v1/customer/announcements", auth.requireCustomer(http.HandlerFunc(overview.customerAnnouncements)))
	mux.Handle("GET /api/v1/admin/announcements", auth.require("settings:read", http.HandlerFunc(overview.adminAnnouncements)))
	mux.Handle("POST /api/v1/admin/announcements", auth.require("settings:write", http.HandlerFunc(overview.saveAnnouncement)))
	mux.Handle("PUT /api/v1/admin/announcements/{id}", auth.require("settings:write", http.HandlerFunc(overview.saveAnnouncement)))
	mux.Handle("DELETE /api/v1/admin/announcements/{id}", auth.require("settings:write", http.HandlerFunc(overview.deleteAnnouncement)))
	mux.Handle("POST /api/v1/customer/services/{id}/actions/{action}", auth.requireCustomer(http.HandlerFunc(portal.serviceAction)))
	mux.Handle("GET /api/v1/customer/services/{id}/runtime", auth.requireCustomer(http.HandlerFunc(portal.serviceRuntime)))
	mux.Handle("GET /api/v1/customer/services/{id}/credential", auth.requireCustomer(http.HandlerFunc(portal.serviceCredential)))
	mux.Handle("POST /api/v1/customer/services/{id}/reset-password", auth.requireCustomer(http.HandlerFunc(portal.resetServicePassword)))
	mux.Handle("POST /api/v1/customer/services/{id}/reinstall", auth.requireCustomer(http.HandlerFunc(portal.reinstallService)))
	mux.Handle("POST /api/v1/customer/services/{id}/port-mappings", auth.requireCustomer(http.HandlerFunc(portal.addPortMapping)))
	mux.Handle("PUT /api/v1/customer/services/{id}/port-mappings/{index}", auth.requireCustomer(http.HandlerFunc(portal.updatePortMapping)))
	mux.Handle("DELETE /api/v1/customer/services/{id}/port-mappings/{index}", auth.requireCustomer(http.HandlerFunc(portal.deletePortMapping)))
	mux.Handle("POST /api/v1/customer/services/{id}/console/{kind}/ticket", auth.requireCustomer(http.HandlerFunc(portal.consoleTicket)))
	mux.Handle("GET /api/v1/customer/services/{id}/console/{kind}", auth.requireCustomer(http.HandlerFunc(portal.consoleProxy)))
	mux.Handle("GET /api/v1/customer/invoices", auth.requireCustomer(http.HandlerFunc(portal.listInvoices)))
	mux.Handle("GET /api/v1/customer/transactions", auth.requireCustomer(http.HandlerFunc(portal.listTransactions)))
	mux.Handle("GET /api/v1/customer/catalog", auth.requireCustomer(http.HandlerFunc(portal.catalogData)))
	mux.Handle("GET /api/v1/customer/orders", auth.requireCustomer(http.HandlerFunc(portal.listOrders)))
	mux.Handle("POST /api/v1/customer/orders", auth.requireVerifiedCustomer(http.HandlerFunc(portal.createOrder)))
	mux.Handle("POST /api/v1/customer/invoices/{id}/checkout", auth.requireVerifiedCustomer(http.HandlerFunc(portal.checkout)))
	mux.Handle("GET /api/v1/customer/tickets", auth.requireCustomer(http.HandlerFunc(operations.customerListTickets)))
	mux.Handle("POST /api/v1/customer/tickets", auth.requireCustomer(http.HandlerFunc(operations.customerCreateTicket)))
	mux.Handle("GET /api/v1/customer/tickets/{id}", auth.requireCustomer(http.HandlerFunc(operations.customerTicketDetail)))
	mux.Handle("POST /api/v1/customer/tickets/{id}/messages", auth.requireCustomer(http.HandlerFunc(operations.customerReplyTicket)))
	mux.Handle("GET /api/v1/customer/tickets/{id}/attachments/{attachment}", auth.requireCustomer(http.HandlerFunc(operations.customerAttachment)))
	mux.Handle("GET /api/v1/customer/wallet", auth.requireCustomer(http.HandlerFunc(market.customerWallet)))
	mux.Handle("POST /api/v1/customer/wallet/topup", auth.requireVerifiedCustomer(http.HandlerFunc(market.customerTopup)))
	mux.Handle("POST /api/v1/customer/invoices/{id}/pay-balance", auth.requireVerifiedCustomer(http.HandlerFunc(market.customerPayWithBalance)))
	mux.Handle("GET /api/v1/customer/market", auth.requireCustomer(http.HandlerFunc(market.market)))
	mux.Handle("GET /api/v1/customer/hosting", auth.requireCustomer(http.HandlerFunc(market.hosting)))
	mux.Handle("POST /api/v1/customer/hosting/nodes", auth.requireVerifiedCustomer(http.HandlerFunc(market.publishNode)))
	mux.Handle("PUT /api/v1/customer/hosting/nodes/{id}", auth.requireCustomer(http.HandlerFunc(market.updateHostedNode)))
	mux.Handle("DELETE /api/v1/customer/hosting/agents/{id}", auth.requireCustomer(http.HandlerFunc(market.dismissHostedAgent)))
	mux.Handle("PUT /api/v1/customer/hosting/nodes/{id}/overcommit", auth.requireCustomer(http.HandlerFunc(market.hostOvercommit)))
	mux.Handle("POST /api/v1/customer/hosting/nodes/{id}/listing", auth.requireCustomer(http.HandlerFunc(market.setHostedListing)))
	mux.Handle("POST /api/v1/customer/hosting/nodes/{id}/retire", auth.requireCustomer(http.HandlerFunc(market.retireHostedNode)))
	mux.Handle("GET /api/v1/customer/hosting/nodes/{id}/templates", auth.requireCustomer(http.HandlerFunc(market.hostedTemplates)))
	mux.Handle("POST /api/v1/customer/hosting/nodes/{id}/plans", auth.requireCustomer(http.HandlerFunc(market.createHostedPlan)))
	mux.Handle("POST /api/v1/customer/hosting/nodes/{id}/stock-capacity", auth.requireCustomer(http.HandlerFunc(market.hostedStockCapacity)))
	mux.Handle("PUT /api/v1/customer/hosting/plans/{id}", auth.requireCustomer(http.HandlerFunc(market.updateHostedPlan)))
	mux.Handle("POST /api/v1/customer/hosting/plans/{id}/enabled", auth.requireCustomer(http.HandlerFunc(market.setHostedPlanEnabled)))
	mux.Handle("GET /api/v1/customer/hosting/tickets", auth.requireCustomer(http.HandlerFunc(operations.hostListTickets)))
	mux.Handle("GET /api/v1/customer/hosting/tickets/{id}", auth.requireCustomer(http.HandlerFunc(operations.hostTicketDetail)))
	mux.Handle("POST /api/v1/customer/hosting/tickets/{id}/messages", auth.requireCustomer(http.HandlerFunc(operations.hostReplyTicket)))
	mux.Handle("GET /api/v1/customer/hosting/tickets/{id}/attachments/{attachment}", auth.requireCustomer(http.HandlerFunc(operations.hostAttachment)))
	mux.Handle("GET /api/v1/customer/hosting/coupons", auth.requireCustomer(http.HandlerFunc(coupons.hostList)))
	mux.Handle("POST /api/v1/customer/hosting/coupons", auth.requireVerifiedCustomer(http.HandlerFunc(coupons.hostCreate)))
	mux.Handle("PUT /api/v1/customer/hosting/coupons/{id}", auth.requireCustomer(http.HandlerFunc(coupons.hostUpdate)))
	mux.Handle("POST /api/v1/customer/coupons/quote", auth.requireCustomer(http.HandlerFunc(coupons.quote)))
	mux.Handle("GET /api/v1/customer/services/{id}/refund", auth.requireCustomer(http.HandlerFunc(coupons.refundQuote)))
	mux.Handle("POST /api/v1/customer/services/{id}/refund", auth.requireCustomer(http.HandlerFunc(coupons.refund)))
	mux.Handle("GET /api/v1/customer/trade", auth.requireCustomer(http.HandlerFunc(trade.market)))
	mux.Handle("POST /api/v1/customer/trade/listings", auth.requireVerifiedCustomer(http.HandlerFunc(trade.createListing)))
	mux.Handle("POST /api/v1/customer/trade/listings/{id}/cancel", auth.requireCustomer(http.HandlerFunc(trade.cancelListing)))
	mux.Handle("POST /api/v1/customer/trade/listings/{id}/buy", auth.requireVerifiedCustomer(http.HandlerFunc(trade.buy)))
	mux.Handle("POST /api/v1/customer/reports", auth.requireVerifiedCustomer(http.HandlerFunc(market.createReport)))
	mux.Handle("GET /api/v1/customer/chat/rooms", auth.requireCustomer(http.HandlerFunc(market.customerChatRooms)))
	mux.Handle("GET /api/v1/customer/chat/rooms/{node}/messages", auth.requireCustomer(http.HandlerFunc(market.customerChatMessages)))
	mux.Handle("POST /api/v1/customer/chat/rooms/{node}/messages", auth.requireVerifiedCustomer(http.HandlerFunc(market.customerPostChat)))
	mux.Handle("GET /api/v1/customer/chat/rooms/{node}/stream", auth.requireCustomer(http.HandlerFunc(market.customerChatStream)))
	// Public on purpose: nodes fetch the agent before they hold any credential.
	mux.HandleFunc("GET /api/v1/agent/download/{file}", agentDownloads(deps.Config.AgentDownloadDir))
	// Public too: agents behind 1:1 NAT learn their public IPv4 from it.
	mux.HandleFunc("GET /api/v1/agent/ip", agentIP(deps.Settings))
	if deps.AgentGateway != nil {
		// Authenticated by the agent bearer token inside the gateway.
		mux.Handle("GET /api/v1/agent/connect", deps.AgentGateway)
	}
	if deps.AgentInternal != nil {
		// Authenticated by the cluster HMAC inside the handler; the public
		// proxies only route /api, /health and /metrics.
		mux.Handle("/internal/v1/agent/", deps.AgentInternal)
	}
	mux.Handle("GET /api/v1/admin/nodes", auth.require("nodes:read", http.HandlerFunc(admin.listNodes)))
	mux.Handle("GET /api/v1/admin/provider-types", auth.require("nodes:read", http.HandlerFunc(admin.listProviderTypes)))
	mux.Handle("GET /api/v1/admin/hosts", auth.require("nodes:read", http.HandlerFunc(metrics.hosts)))
	mux.Handle("GET /api/v1/admin/hosts/{id}/probe", auth.require("nodes:read", http.HandlerFunc(admin.hostProbe)))
	mux.Handle("GET /api/v1/admin/overview", auth.require("customers:read", http.HandlerFunc(metrics.overview)))
	mux.Handle("POST /api/v1/admin/nodes", auth.require("nodes:write", http.HandlerFunc(admin.createNode)))
	mux.Handle("POST /api/v1/admin/nodes/tls-probe", auth.require("nodes:write", http.HandlerFunc(admin.tlsProbe)))
	mux.Handle("GET /api/v1/admin/agent-enrollments", auth.require("nodes:read", http.HandlerFunc(admin.listEnrollments)))
	mux.Handle("DELETE /api/v1/admin/agent-enrollments/{id}", auth.require("nodes:write", http.HandlerFunc(admin.dismissEnrollment)))
	mux.Handle("GET /api/v1/admin/regions/all", auth.require("nodes:read", http.HandlerFunc(admin.listAllRegions)))
	mux.Handle("POST /api/v1/admin/regions", auth.require("nodes:write", http.HandlerFunc(admin.createRegion)))
	mux.Handle("PUT /api/v1/admin/regions/{id}", auth.require("nodes:write", http.HandlerFunc(admin.updateRegion)))
	mux.Handle("POST /api/v1/admin/nodes/{id}/test", auth.require("nodes:write", http.HandlerFunc(admin.testNode)))
	mux.Handle("PUT /api/v1/admin/nodes/{id}", auth.require("nodes:write", http.HandlerFunc(admin.updateNode)))
	mux.Handle("DELETE /api/v1/admin/nodes/{id}", auth.require("nodes:write", http.HandlerFunc(admin.deleteNode)))
	mux.Handle("GET /api/v1/admin/plans", auth.require("plans:read", http.HandlerFunc(admin.listPlans)))
	mux.Handle("GET /api/v1/admin/templates", auth.require("plans:read", http.HandlerFunc(admin.listTemplates)))
	mux.Handle("POST /api/v1/admin/plans", auth.require("plans:write", http.HandlerFunc(admin.createPlan)))
	mux.Handle("POST /api/v1/admin/plans/stock-capacity", auth.require("plans:read", http.HandlerFunc(admin.planStockCapacity)))
	mux.Handle("PATCH /api/v1/admin/plans/{id}", auth.require("plans:write", http.HandlerFunc(admin.updatePlan)))
	mux.Handle("PUT /api/v1/admin/plans/{id}", auth.require("plans:write", http.HandlerFunc(admin.replacePlan)))
	mux.Handle("GET /api/v1/admin/customers", auth.require("customers:read", http.HandlerFunc(billing.listCustomers)))
	mux.Handle("POST /api/v1/admin/customers", auth.require("customers:write", http.HandlerFunc(billing.createCustomer)))
	mux.Handle("PATCH /api/v1/admin/customers/{id}", auth.require("customers:write", http.HandlerFunc(billing.updateCustomer)))
	mux.Handle("POST /api/v1/admin/customers/{id}/password-reset", auth.require("customers:write", http.HandlerFunc(auth.staffIssueCustomerReset)))
	mux.Handle("GET /api/v1/admin/regions", auth.require("nodes:read", http.HandlerFunc(billing.listRegions)))
	mux.Handle("GET /api/v1/admin/orders", auth.require("orders:read", http.HandlerFunc(billing.listOrders)))
	mux.Handle("POST /api/v1/admin/orders", auth.require("orders:write", http.HandlerFunc(billing.createOrder)))
	mux.Handle("GET /api/v1/admin/invoices", auth.require("billing:read", http.HandlerFunc(billing.listInvoices)))
	mux.Handle("GET /api/v1/admin/transactions", auth.require("billing:read", http.HandlerFunc(billing.listTransactions)))
	mux.Handle("POST /api/v1/admin/invoices/{id}/pay", auth.require("billing:write", http.HandlerFunc(billing.recordManualPayment)))
	mux.Handle("GET /api/v1/admin/settings/payment", auth.require("billing:read", http.HandlerFunc(adminSettings.payment)))
	mux.Handle("PUT /api/v1/admin/settings/payment", auth.require("billing:write", http.HandlerFunc(adminSettings.updatePayment)))
	mux.Handle("GET /api/v1/admin/settings/site", auth.require("settings:read", http.HandlerFunc(adminSettings.site)))
	mux.Handle("PUT /api/v1/admin/settings/site", auth.require("settings:write", http.HandlerFunc(adminSettings.updateSite)))
	mux.Handle("POST /api/v1/admin/settings/site/test-mail", auth.require("settings:write", http.HandlerFunc(adminSettings.testMail)))
	mux.Handle("GET /api/v1/admin/services", auth.require("services:read", http.HandlerFunc(automation.listServices)))
	mux.Handle("GET /api/v1/admin/jobs", auth.require("services:read", http.HandlerFunc(automation.listJobs)))
	mux.Handle("POST /api/v1/admin/jobs/{id}/retry", auth.require("services:write", http.HandlerFunc(automation.retryJob)))
	mux.Handle("POST /api/v1/admin/services/{id}/actions/{action}", auth.require("services:write", http.HandlerFunc(automation.serviceAction)))
	mux.Handle("GET /api/v1/admin/tickets", auth.require("tickets:read", http.HandlerFunc(operations.adminListTickets)))
	mux.Handle("GET /api/v1/admin/tickets/{id}", auth.require("tickets:read", http.HandlerFunc(operations.adminTicketDetail)))
	mux.Handle("POST /api/v1/admin/tickets/{id}/messages", auth.require("tickets:write", http.HandlerFunc(operations.adminReplyTicket)))
	mux.Handle("GET /api/v1/admin/tickets/{id}/attachments/{attachment}", auth.require("tickets:read", http.HandlerFunc(operations.adminAttachment)))
	mux.Handle("PATCH /api/v1/admin/tickets/{id}", auth.require("tickets:write", http.HandlerFunc(operations.adminUpdateTicket)))
	mux.Handle("GET /api/v1/admin/marketplace/nodes", auth.require("nodes:read", http.HandlerFunc(market.adminHostedNodes)))
	mux.Handle("POST /api/v1/admin/marketplace/nodes/{id}/clear", auth.require("nodes:write", http.HandlerFunc(market.adminClearNode)))
	mux.Handle("PUT /api/v1/admin/marketplace/nodes/{id}/hold", auth.require("nodes:write", http.HandlerFunc(market.adminHoldClearance)))
	mux.Handle("POST /api/v1/admin/marketplace/nodes/{id}/listing", auth.require("nodes:write", http.HandlerFunc(market.adminSetListing)))
	mux.Handle("GET /api/v1/admin/trade/listings", auth.require("services:read", http.HandlerFunc(trade.adminListings)))
	mux.Handle("POST /api/v1/admin/trade/listings/{id}/cancel", auth.require("services:write", http.HandlerFunc(trade.adminCancel)))
	mux.Handle("GET /api/v1/admin/coupons", auth.require("plans:read", http.HandlerFunc(coupons.adminList)))
	mux.Handle("POST /api/v1/admin/coupons", auth.require("plans:write", http.HandlerFunc(coupons.adminCreate)))
	mux.Handle("PUT /api/v1/admin/coupons/{id}", auth.require("plans:write", http.HandlerFunc(coupons.adminUpdate)))
	mux.Handle("GET /api/v1/admin/reports", auth.require("tickets:read", http.HandlerFunc(market.adminReports)))
	mux.Handle("POST /api/v1/admin/reports/{id}/resolve", auth.require("tickets:write", http.HandlerFunc(market.adminResolveReport)))
	mux.Handle("PUT /api/v1/admin/nodes/{id}/overcommit", auth.require("nodes:write", http.HandlerFunc(market.adminOvercommit)))
	mux.Handle("GET /api/v1/admin/overcommit-limits", auth.require("nodes:read", http.HandlerFunc(market.adminOvercommitLimits)))
	mux.Handle("PUT /api/v1/admin/marketplace/nodes/{id}/capacity-cap", auth.require("nodes:write", http.HandlerFunc(market.adminCapacityCap)))
	mux.Handle("GET /api/v1/admin/chat/rooms/{node}/mutes", auth.require("tickets:read", http.HandlerFunc(market.adminChatMutes)))
	mux.Handle("POST /api/v1/admin/chat/rooms/{node}/mutes", auth.require("tickets:write", http.HandlerFunc(market.adminMuteChat)))
	mux.Handle("DELETE /api/v1/admin/chat/rooms/{node}/mutes/{account}", auth.require("tickets:write", http.HandlerFunc(market.adminUnmuteChat)))
	mux.Handle("GET /api/v1/admin/chat/rooms", auth.require("tickets:read", http.HandlerFunc(market.adminChatRooms)))
	mux.Handle("GET /api/v1/admin/chat/rooms/{node}/messages", auth.require("tickets:read", http.HandlerFunc(market.adminChatMessages)))
	mux.Handle("POST /api/v1/admin/chat/rooms/{node}/messages", auth.require("tickets:write", http.HandlerFunc(market.adminPostChat)))
	mux.Handle("GET /api/v1/admin/chat/rooms/{node}/stream", auth.require("tickets:read", http.HandlerFunc(market.adminChatStream)))
	mux.Handle("GET /api/v1/admin/customers/{id}/wallet", auth.require("customers:read", http.HandlerFunc(market.adminWallet)))
	mux.Handle("POST /api/v1/admin/customers/{id}/wallet/adjust", auth.require("billing:write", http.HandlerFunc(market.adminAdjustWallet)))
	mux.Handle("GET /api/v1/admin/audit-logs", auth.require("audit:read", http.HandlerFunc(operations.adminAuditLogs)))
	mux.HandleFunc("POST /api/v1/webhooks/payments/generic", billing.paymentWebhook)
	mux.HandleFunc("GET /api/v1/webhooks/payments/epay", billing.epayWebhook)
	mux.HandleFunc("POST /api/v1/webhooks/payments/epay", billing.epayWebhook)
	mux.HandleFunc("POST /api/v1/webhooks/payments/alipay", billing.alipayWebhook)

	return requestLog(deps.Logger, securityHeaders(surfaceGate(installationGate(deps.Settings, mux)))), nil
}

func installationGate(runtime *settings.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if runtime.Current().Installed || r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" || r.URL.Path == "/api/v1/meta" || r.URL.Path == "/api/v1/install" {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "installation_required", "message": "请先完成系统安装"})
	})
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

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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

// surfaceGate keeps the customer portal and the admin console apart when
// the web proxy says which one a request came through (X-VPSBill-Surface):
// the portal cannot reach staff APIs or run the installer, and the admin
// address does not serve customer APIs. Requests without the header, from
// inside the network, are not restricted.
func surfaceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !surfaceAllows(r.Header.Get("X-VPSBill-Surface"), r.Method, r.URL.Path) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func surfaceAllows(surface, method, path string) bool {
	switch surface {
	case "portal":
		switch {
		case strings.HasPrefix(path, "/api/v1/admin/"), strings.HasPrefix(path, "/api/v1/auth/"):
			return false
		case path == "/api/v1/install" && method != http.MethodGet:
			return false
		}
	case "admin":
		if strings.HasPrefix(path, "/api/v1/customer/") {
			return false
		}
	}
	return true
}
