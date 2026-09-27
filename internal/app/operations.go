package app

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"vpsbill/internal/notify"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

type operationsAPI struct {
	store    *postgres.OperationsStore
	settings *settings.Manager
	notifier *notify.Notifier
}

func newOperationsAPI(store *postgres.OperationsStore, runtime *settings.Manager, notifier *notify.Notifier) *operationsAPI {
	return &operationsAPI{store: store, settings: runtime, notifier: notifier}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (a *operationsAPI) maxAttachmentMB() int { return a.settings.Current().TicketAttachmentMaxMB }

// notifyAsync runs a notification outside the request so a slow database
// query for recipients never delays the response.
func (a *operationsAPI) notifyAsync(send func(ctx context.Context, notifier *notify.Notifier)) {
	if a.notifier == nil {
		return
	}
	go send(context.Background(), a.notifier)
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
	uploads, ok := readTicketRequest(w, r, a.maxAttachmentMB(), &input)
	if !ok {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	result, err := a.store.CreateTicket(r.Context(), identity.AccountID, identity.UserID, strings.TrimSpace(input.ServiceID), input.Subject, input.Priority, input.Body, remoteIP(r), r.UserAgent(), uploads...)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "ticket_create_failed", "message": "工单主题、优先级、内容或关联服务无效"})
		return
	}
	message := result.Messages[0]
	a.notifyAsync(func(ctx context.Context, notifier *notify.Notifier) {
		notifier.TicketCreated(ctx, result.Ticket.ID, message.ID, message.Body)
	})
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
	uploads, ok := readTicketRequest(w, r, a.maxAttachmentMB(), &input)
	if !ok {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	ticketID := r.PathValue("id")
	message, err := a.store.ReplyTicket(r.Context(), ticketID, identity.AccountID, "customer", identity.UserID, input.Body, false, uploads...)
	if err == nil {
		a.notifyAsync(func(ctx context.Context, notifier *notify.Notifier) {
			notifier.TicketReplied(ctx, ticketID, message.ID, "customer", message.Body, false)
		})
	}
	writeTicketMutation(w, message, err)
}

func (a *operationsAPI) customerAttachment(w http.ResponseWriter, r *http.Request) {
	a.attachment(w, r, customerPrincipalFromContext(r.Context()).AccountID)
}

func (a *operationsAPI) adminAttachment(w http.ResponseWriter, r *http.Request) {
	a.attachment(w, r, "")
}

func (a *operationsAPI) attachment(w http.ResponseWriter, r *http.Request, accountID string) {
	ticketID, attachmentID := r.PathValue("id"), r.PathValue("attachment")
	if !uuidPattern.MatchString(ticketID) || !uuidPattern.MatchString(attachmentID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "attachment_not_found"})
		return
	}
	item, data, err := a.store.TicketAttachmentData(r.Context(), ticketID, attachmentID, accountID)
	if errors.Is(err, postgres.ErrAttachmentNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "attachment_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	serveAttachment(w, item, data)
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
	uploads, ok := readTicketRequest(w, r, a.maxAttachmentMB(), &input)
	if !ok {
		return
	}
	identity := principalFromContext(r.Context())
	ticketID := r.PathValue("id")
	message, err := a.store.ReplyTicket(r.Context(), ticketID, "", "staff", identity.UserID, input.Body, input.Internal, uploads...)
	if err == nil {
		a.notifyAsync(func(ctx context.Context, notifier *notify.Notifier) {
			notifier.TicketReplied(ctx, ticketID, message.ID, "staff", message.Body, message.Internal)
		})
	}
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

// Hosted-instance tickets as seen by the host: the host answers first and
// never sees staff notes.

func (a *operationsAPI) hostListTickets(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListHostTickets(r.Context(), customerPrincipalFromContext(r.Context()).AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (a *operationsAPI) hostTicketDetail(w http.ResponseWriter, r *http.Request) {
	result, err := a.store.HostTicketDetail(r.Context(), r.PathValue("id"), customerPrincipalFromContext(r.Context()).AccountID)
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

func (a *operationsAPI) hostReplyTicket(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body string `json:"body"`
	}
	uploads, ok := readTicketRequest(w, r, a.maxAttachmentMB(), &input)
	if !ok {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	ticketID := r.PathValue("id")
	message, err := a.store.ReplyTicket(r.Context(), ticketID, identity.AccountID, "host", identity.UserID, input.Body, false, uploads...)
	if err == nil {
		a.notifyAsync(func(ctx context.Context, notifier *notify.Notifier) {
			notifier.TicketReplied(ctx, ticketID, message.ID, "host", message.Body, false)
		})
	}
	writeTicketMutation(w, message, err)
}

func (a *operationsAPI) hostAttachment(w http.ResponseWriter, r *http.Request) {
	ticketID, attachmentID := r.PathValue("id"), r.PathValue("attachment")
	if !uuidPattern.MatchString(ticketID) || !uuidPattern.MatchString(attachmentID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "attachment_not_found"})
		return
	}
	item, data, err := a.store.HostTicketAttachmentData(r.Context(), ticketID, attachmentID, customerPrincipalFromContext(r.Context()).AccountID)
	if errors.Is(err, postgres.ErrAttachmentNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "attachment_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	serveAttachment(w, item, data)
}
