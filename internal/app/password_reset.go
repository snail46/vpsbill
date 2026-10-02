package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vpsbill/internal/mail"
	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

const (
	selfResetValidity  = 30 * time.Minute
	staffResetValidity = 24 * time.Hour
	selfResetsPerHour  = 3
)

func (a *authenticator) resetLink(token string) string {
	return strings.TrimRight(a.settings.Current().PublicURL, "/") + "/portal/reset-password?token=" + token
}

// customerRequestPasswordReset mails a reset link. The response is the same
// whether or not the address belongs to a customer.
func (a *authenticator) customerRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	runtime := a.settings.Current()
	if !runtime.SMTP.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "password_reset_unavailable", "message": "站点暂未开通邮件找回密码，请联系客服重置"})
		return
	}
	if !validEmail(input.Email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请输入有效的邮箱地址"})
		return
	}
	if allowed, err := a.store.LoginAllowed(r.Context(), input.Email, remoteIP(r)); err != nil || !allowed {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "message": "请求过于频繁，请稍后再试"})
		return
	}
	accepted := map[string]any{"data": map[string]string{"message": "如果该邮箱已注册，重置链接已发送，30 分钟内有效"}}
	identity, err := a.store.CustomerByEmail(r.Context(), input.Email)
	if err != nil || identity.Status != "active" || identity.AccountStatus != "active" {
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	if count, err := a.store.RecentPasswordResets(r.Context(), identity.UserID); err != nil || count >= selfResetsPerHour {
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	token, hash, err := security.NewToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if err := a.store.CreatePasswordReset(r.Context(), identity.UserID, hash, "self", time.Now().Add(selfResetValidity)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), identity.UserID, "customer", "password.reset_requested", remoteIP(r), r.UserAgent())
	// Send in the background so the response time does not reveal whether
	// the address is registered.
	link, to, site, lang := a.resetLink(token), identity.Email, runtime.AppName, a.mailLang(r, identity.Locale)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		subject := site + " 密码重置"
		body := "您好，\n\n我们收到了重置 " + site + " 账户密码的请求。请在 30 分钟内打开下面的链接设置新密码：\n\n" + link +
			"\n\n如果不是您本人操作，请忽略这封邮件，原密码仍然有效。\n"
		if lang == "en" {
			subject = site + ": reset your password"
			body = "Hello,\n\nWe received a request to reset the password of your " + site + " account. Open the link below within 30 minutes to set a new password:\n\n" + link +
				"\n\nIf this was not you, please ignore this email; your password stays as it is.\n"
		}
		if err := mail.Send(ctx, runtime.SMTP, to, subject, body); err != nil {
			slog.Default().Warn("send password reset mail", "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, accepted)
}

// customerResetPassword sets a new password from a reset link and signs the
// customer out of every session.
func (a *authenticator) customerResetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	userID, err := a.store.ResetPassword(r.Context(), security.HashToken(strings.TrimSpace(input.Token)), hash)
	if errors.Is(err, postgres.ErrResetTokenInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_token", "message": "重置链接无效或已过期，请重新申请"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), userID, "customer", "password.reset", remoteIP(r), r.UserAgent())
	w.WriteHeader(http.StatusNoContent)
}

// staffIssueCustomerReset creates a reset link for a customer account that
// staff hand over themselves, for sites without outgoing mail.
func (a *authenticator) staffIssueCustomerReset(w http.ResponseWriter, r *http.Request) {
	userID, email, err := a.store.AccountOwnerLogin(r.Context(), r.PathValue("id"))
	if errors.Is(err, postgres.ErrCustomerNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "customer_login_not_found", "message": "该客户没有可用的登录账号"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	token, hash, err := security.NewToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	expiresAt := time.Now().Add(staffResetValidity)
	if err := a.store.CreatePasswordReset(r.Context(), userID, hash, "staff", expiresAt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), principalFromContext(r.Context()).UserID, "staff", "customer.password_reset_link_issued", remoteIP(r), r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"email": email, "link": a.resetLink(token), "expires_at": expiresAt}})
}
