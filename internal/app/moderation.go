package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/store/postgres"
)

// ---- Reports, chat mutes and capacity caps (hosting center moderation) ----

func (a *marketplaceAPI) createReport(w http.ResponseWriter, r *http.Request) {
	var input postgres.ReportInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !uuidPattern.MatchString(input.NodeID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	id, err := a.store.CreateReport(r.Context(), identity.AccountID, identity.UserID, input)
	var rule *postgres.HostedOrderError
	switch {
	case errors.As(err, &rule):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": rule.Message})
	case errors.Is(err, postgres.ErrHostedNodeNotFound), errors.Is(err, postgres.ErrNotChatMember):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id, "message": "举报已提交，管理员会尽快处理"}})
	}
}

func (a *marketplaceAPI) adminReports(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.Reports(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"reports": rows, "reasons": postgres.ReportReasons}})
}

func (a *marketplaceAPI) adminResolveReport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
	}
	if !uuidPattern.MatchString(r.PathValue("id")) || !decodeJSON(w, r, &input) {
		return
	}
	err := a.store.ResolveReport(r.Context(), r.PathValue("id"), principalFromContext(r.Context()).UserID, input.Status, input.Resolution)
	var rule *postgres.HostedOrderError
	switch {
	case errors.As(err, &rule):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": rule.Message})
	case errors.Is(err, postgres.ErrReportNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "report_not_found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"status": input.Status}})
	}
}

func (a *marketplaceAPI) adminCapacityCap(w http.ResponseWriter, r *http.Request) {
	var input postgres.CapacityCap
	if !uuidPattern.MatchString(r.PathValue("id")) || !decodeJSON(w, r, &input) {
		return
	}
	if input.VCPU != nil && *input.VCPU < 1 || input.RAMMB != nil && *input.RAMMB < 64 || input.DiskGB != nil && *input.DiskGB < 1 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "核定资源无效"})
		return
	}
	err := a.store.SetCapacityCap(r.Context(), r.PathValue("id"), principalFromContext(r.Context()).UserID, input)
	if errors.Is(err, postgres.ErrHostedNodeNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": input})
}

func (a *marketplaceAPI) adminChatMutes(w http.ResponseWriter, r *http.Request) {
	if !uuidPattern.MatchString(r.PathValue("node")) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "room_not_found"})
		return
	}
	mutes, err := a.store.ChatMutes(r.Context(), r.PathValue("node"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": mutes})
}

func (a *marketplaceAPI) adminMuteChat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AccountID string `json:"account_id"`
		Hours     int    `json:"hours"`
		Reason    string `json:"reason"`
	}
	if !uuidPattern.MatchString(r.PathValue("node")) || !decodeJSON(w, r, &input) {
		return
	}
	if !uuidPattern.MatchString(input.AccountID) || input.Hours < 1 || input.Hours > 24*365 || len([]rune(strings.TrimSpace(input.Reason))) > 200 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "禁言参数无效"})
		return
	}
	until := time.Now().Add(time.Duration(input.Hours) * time.Hour)
	if err := a.store.MuteChat(r.Context(), r.PathValue("node"), input.AccountID, principalFromContext(r.Context()).UserID, input.Reason, until); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"until": until}})
}

func (a *marketplaceAPI) adminUnmuteChat(w http.ResponseWriter, r *http.Request) {
	if !uuidPattern.MatchString(r.PathValue("node")) || !uuidPattern.MatchString(r.PathValue("account")) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	if err := a.store.UnmuteChat(r.Context(), r.PathValue("node"), r.PathValue("account")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
