package app

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
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
	_, _ = fmt.Fprintf(w, "# HELP vpsbill_accounts Active billing accounts.\n# TYPE vpsbill_accounts gauge\nvpsbill_accounts %d\n# HELP vpsbill_open_invoices Open invoices.\n# TYPE vpsbill_open_invoices gauge\nvpsbill_open_invoices %d\n# TYPE vpsbill_provisioning_jobs gauge\nvpsbill_provisioning_jobs %d\n# TYPE vpsbill_failed_jobs gauge\nvpsbill_failed_jobs %d\n# TYPE vpsbill_active_services gauge\nvpsbill_active_services %d\n# TYPE vpsbill_unhealthy_nodes gauge\nvpsbill_unhealthy_nodes %d\n# TYPE vpsbill_open_tickets gauge\nvpsbill_open_tickets %d\n# TYPE vpsbill_pending_outbox gauge\nvpsbill_pending_outbox %d\n", snapshot.Accounts, snapshot.OpenInvoices, snapshot.ProvisioningJobs, snapshot.FailedJobs, snapshot.ActiveServices, snapshot.UnhealthyNodes, snapshot.OpenTickets, snapshot.PendingOutbox)
}

func (m *metricsAPI) overview(w http.ResponseWriter, r *http.Request) {
	value, err := m.store.Overview(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "overview_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

func (m *metricsAPI) hosts(w http.ResponseWriter, r *http.Request) {
	value, err := m.store.Hosts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "hosts_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}
