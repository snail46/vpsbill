package app

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"vpsbill/internal/store/postgres"
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

// serviceAction lets staff power a service or terminate it immediately.
func (a *adminAutomation) serviceAction(w http.ResponseWriter, r *http.Request) {
	identity := principalFromContext(r.Context())
	jobID, err := a.store.QueueAdminServiceAction(r.Context(), identity.UserID, r.PathValue("id"), r.PathValue("action"), remoteIP(r), r.UserAgent())
	switch {
	case errors.Is(err, postgres.ErrServiceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
	case errors.Is(err, postgres.ErrServiceActionUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_unavailable", "message": "当前服务状态不允许此操作"})
	case errors.Is(err, postgres.ErrActionInProgress):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_in_progress", "message": "该服务已有同类任务在执行"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"job_id": jobID}})
	}
}
