package app

import (
	"errors"
	"net/http"
	"strings"

	"clicd-billing/internal/store/postgres"
)

type operationsAPI struct{ store *postgres.OperationsStore }

func newOperationsAPI(store *postgres.OperationsStore) *operationsAPI {
	return &operationsAPI{store: store}
}

func (a *operationsAPI) customerListTickets(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := a.store.ListCustomerTickets(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (a *operationsAPI) customerCreateTicket(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ServiceID string `json:"service_id"`
		Subject   string `json:"subject"`
		Priority  string `json:"priority"`
		Body      string `json:"body"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	result, err := a.store.CreateTicket(r.Context(), identity.AccountID, identity.UserID, strings.TrimSpace(input.ServiceID), input.Subject, input.Priority, input.Body, remoteIP(r), r.UserAgent())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "ticket_create_failed", "message": "工单主题、优先级、内容或关联服务无效"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": result})
}

func (a *operationsAPI) customerTicketDetail(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	result, err := a.store.TicketDetail(r.Context(), r.PathValue("id"), identity.AccountID, false)
	if errors.Is(err, postgres.ErrTicketNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (a *operationsAPI) customerReplyTicket(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	message, err := a.store.ReplyTicket(r.Context(), r.PathValue("id"), identity.AccountID, "customer", identity.UserID, input.Body, false)
	writeTicketMutation(w, message, err)
}

func (a *operationsAPI) adminListTickets(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListTickets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (a *operationsAPI) adminTicketDetail(w http.ResponseWriter, r *http.Request) {
	result, err := a.store.TicketDetail(r.Context(), r.PathValue("id"), "", true)
	if errors.Is(err, postgres.ErrTicketNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (a *operationsAPI) adminReplyTicket(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body     string `json:"body"`
		Internal bool   `json:"internal"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := principalFromContext(r.Context())
	message, err := a.store.ReplyTicket(r.Context(), r.PathValue("id"), "", "staff", identity.UserID, input.Body, input.Internal)
	writeTicketMutation(w, message, err)
}

func (a *operationsAPI) adminUpdateTicket(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := principalFromContext(r.Context())
	err := a.store.UpdateTicketStatus(r.Context(), r.PathValue("id"), identity.UserID, input.Status)
	if errors.Is(err, postgres.ErrTicketNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "ticket_update_failed", "message": "工单状态无效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"status": input.Status}})
}

func (a *operationsAPI) adminAuditLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListAuditLogs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func writeTicketMutation(w http.ResponseWriter, message postgres.TicketMessage, err error) {
	switch {
	case errors.Is(err, postgres.ErrTicketNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket_not_found"})
	case errors.Is(err, postgres.ErrTicketClosed):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ticket_closed", "message": "已关闭工单不能继续回复"})
	case err != nil:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "ticket_reply_failed", "message": "回复内容无效"})
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"data": message})
	}
}
