package app

import (
	"errors"
	"net/http"
	"strings"

	"vpsbill/internal/settings"
)

type adminSettings struct{ settings *settings.Manager }

func (a adminSettings) payment(w http.ResponseWriter, _ *http.Request) {
	runtime := a.settings.Current()
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"gateway": a.settings.PaymentGatewayView(), "callbacks": map[string]string{
		"generic":    strings.TrimRight(runtime.PublicURL, "/") + "/api/v1/webhooks/payments/generic",
		"alipay_f2f": strings.TrimRight(runtime.PublicURL, "/") + "/api/v1/webhooks/payments/alipay",
		"epay":       strings.TrimRight(runtime.PublicURL, "/") + "/api/v1/webhooks/payments/epay",
	}}})
}

func (a adminSettings) updatePayment(w http.ResponseWriter, r *http.Request) {
	var input settings.PaymentGatewayInput
	if !decodeJSON(w, r, &input) {
		return
	}
	view, err := a.settings.UpdatePaymentGateway(r.Context(), input, principalFromContext(r.Context()).UserID)
	if errors.Is(err, settings.ErrInvalidSettings) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "settings_update_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": view})
}
