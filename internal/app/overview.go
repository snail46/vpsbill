package app

import (
	"errors"
	"net/http"

	"vpsbill/internal/store/postgres"
)

// overviewAPI serves the customer overview and the announcements staff
// publish on it.
type overviewAPI struct {
	announcements *postgres.AnnouncementStore
	billing       *postgres.BillingStore
	market        *postgres.MarketplaceStore
}

// customerOverview returns the balance, hosting earnings and published
// announcements in one call.
func (o *overviewAPI) customerOverview(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	wallet, err := o.billing.Wallet(r.Context(), identity.AccountID, 1)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	earnings, err := o.market.HostEarnings(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	announcements, err := o.announcements.List(r.Context(), true, 5)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"balance_minor": wallet.BalanceMinor, "currency": wallet.Currency, "hosting": earnings, "announcements": announcements,
	}})
}

func (o *overviewAPI) customerAnnouncements(w http.ResponseWriter, r *http.Request) {
	o.listAnnouncements(w, r, true)
}

func (o *overviewAPI) adminAnnouncements(w http.ResponseWriter, r *http.Request) {
	o.listAnnouncements(w, r, false)
}

func (o *overviewAPI) listAnnouncements(w http.ResponseWriter, r *http.Request, publishedOnly bool) {
	items, err := o.announcements.List(r.Context(), publishedOnly, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (o *overviewAPI) saveAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id != "" && !uuidPattern.MatchString(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "announcement_not_found"})
		return
	}
	var input postgres.Announcement
	if !decodeJSON(w, r, &input) {
		return
	}
	saved, err := o.announcements.Save(r.Context(), id, input)
	switch {
	case errors.Is(err, postgres.ErrAnnouncementInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "标题 1–120 字，正文不超过 4000 字"})
	case errors.Is(err, postgres.ErrAnnouncementNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "announcement_not_found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": saved})
	}
}

func (o *overviewAPI) deleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	if !uuidPattern.MatchString(r.PathValue("id")) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "announcement_not_found"})
		return
	}
	err := o.announcements.Delete(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, postgres.ErrAnnouncementNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "announcement_not_found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// setAutoRenew switches balance renewal for one of the customer's
// instances.
func (p *customerPortal) setAutoRenew(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !uuidPattern.MatchString(r.PathValue("id")) || !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	if err := p.store.SetAutoRenew(r.Context(), identity.AccountID, r.PathValue("id"), input.Enabled); err != nil {
		p.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": input})
}
