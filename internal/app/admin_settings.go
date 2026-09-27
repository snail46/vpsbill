package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/mail"
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

func (a adminSettings) site(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.settings.SiteView()})
}

func (a adminSettings) updateSite(w http.ResponseWriter, r *http.Request) {
	var input settings.SiteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	view, err := a.settings.UpdateSite(r.Context(), input, principalFromContext(r.Context()).UserID)
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

// testMail sends a message with the saved SMTP settings to the signed-in
// administrator, so the result shows up in their own mailbox.
func (a adminSettings) testMail(w http.ResponseWriter, r *http.Request) {
	runtime := a.settings.Current()
	if !runtime.SMTP.Configured() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "smtp_not_configured", "message": "请先保存 SMTP 设置"})
		return
	}
	to := principalFromContext(r.Context()).Email
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	body := "这是一封测试邮件，说明 " + runtime.AppName + " 可以正常发送密码找回邮件。\n\n" + runtime.PublicURL + "\n"
	if err := mail.Send(ctx, runtime.SMTP, to, runtime.AppName+" 邮件设置测试", body); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "smtp_failed", "message": "发送失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"to": to}})
}
