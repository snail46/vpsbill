package app

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"clicd-billing/internal/settings"
	"clicd-billing/internal/store/postgres"
)

type metricsAPI struct {
	store    *postgres.MonitoringStore
	settings *settings.Manager
}

func newMetricsAPI(store *postgres.MonitoringStore, runtime *settings.Manager) *metricsAPI {
	return &metricsAPI{store: store, settings: runtime}
}
func (m *metricsAPI) serve(w http.ResponseWriter, r *http.Request) {
	token := m.settings.Current().MetricsToken
	if token != "" {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication_required"})
			return
		}
	}
	snapshot, err := m.store.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "metrics_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP clicd_billing_accounts Active billing accounts.\n# TYPE clicd_billing_accounts gauge\nclicd_billing_accounts %d\n# HELP clicd_billing_open_invoices Open invoices.\n# TYPE clicd_billing_open_invoices gauge\nclicd_billing_open_invoices %d\n# TYPE clicd_billing_provisioning_jobs gauge\nclicd_billing_provisioning_jobs %d\n# TYPE clicd_billing_failed_jobs gauge\nclicd_billing_failed_jobs %d\n# TYPE clicd_billing_active_services gauge\nclicd_billing_active_services %d\n# TYPE clicd_billing_unhealthy_nodes gauge\nclicd_billing_unhealthy_nodes %d\n# TYPE clicd_billing_open_tickets gauge\nclicd_billing_open_tickets %d\n# TYPE clicd_billing_pending_outbox gauge\nclicd_billing_pending_outbox %d\n", snapshot.Accounts, snapshot.OpenInvoices, snapshot.ProvisioningJobs, snapshot.FailedJobs, snapshot.ActiveServices, snapshot.UnhealthyNodes, snapshot.OpenTickets, snapshot.PendingOutbox)
}
