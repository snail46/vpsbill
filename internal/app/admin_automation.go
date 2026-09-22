package app

import (
	"errors"
	"net/http"

	"clicd-billing/internal/store/postgres"
	"github.com/jackc/pgx/v5"
)

type adminAutomation struct{ store *postgres.ProvisioningStore }

func newAdminAutomation(store *postgres.ProvisioningStore) *adminAutomation {
	return &adminAutomation{store: store}
}

func (a *adminAutomation) listServices(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (a *adminAutomation) listJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListJobs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (a *adminAutomation) retryJob(w http.ResponseWriter, r *http.Request) {
	err := a.store.RetryJob(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "job_not_retryable", "message": "仅失败或已终止的任务可以重试"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
