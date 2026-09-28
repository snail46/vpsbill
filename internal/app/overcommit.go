package app

import (
	"errors"
	"net/http"

	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// overcommitLimits are the platform's maximum oversell ratios.
func overcommitLimits(runtime settings.Runtime) postgres.OvercommitLimits {
	m := runtime.Marketplace
	return postgres.OvercommitLimits{CPU: m.MaxOvercommitCPU, RAM: m.MaxOvercommitRAM, Disk: m.MaxOvercommitDisk, Traffic: m.MaxOvercommitTraffic}
}

// setOvercommit changes a node's oversell ratios; ownerID limits it to the
// host's own node, empty means staff.
func (a *marketplaceAPI) setOvercommit(w http.ResponseWriter, r *http.Request, ownerID string) {
	var input postgres.Overcommit
	if !uuidPattern.MatchString(r.PathValue("id")) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	err := a.catalog.SetOvercommit(r.Context(), r.PathValue("id"), ownerID, input, overcommitLimits(a.settings.Current()))
	switch {
	case errors.Is(err, postgres.ErrOvercommitInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()[len(postgres.ErrOvercommitInvalid.Error())+2:]})
	case errors.Is(err, postgres.ErrHostedNodeNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": input})
	}
}

func (a *marketplaceAPI) hostOvercommit(w http.ResponseWriter, r *http.Request) {
	a.setOvercommit(w, r, customerPrincipalFromContext(r.Context()).AccountID)
}

func (a *marketplaceAPI) adminOvercommit(w http.ResponseWriter, r *http.Request) {
	a.setOvercommit(w, r, "")
}

func (a *marketplaceAPI) adminOvercommitLimits(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": overcommitLimits(a.settings.Current())})
}
