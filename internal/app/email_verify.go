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
	emailVerifyValidity = 24 * time.Hour
	emailVerifyPerHour  = 3
)

// sendVerification mails a link that confirms the customer owns the
// address. It is sent in the background.
// lang is the language the mail is written in (see mailLang).
func (a *authenticator) sendVerification(ctx context.Context, userID, email, lang string) error {
	runtime := a.settings.Current()
	if !runtime.SMTP.Configured() {
		return errors.New("smtp is not configured")
	}
	token, hash, err := security.NewToken()
	if err != nil {
		return err
	}
	if err := a.store.CreateEmailVerification(ctx, userID, hash, time.Now().Add(emailVerifyValidity)); err != nil {
		return err
	}
	link := strings.TrimRight(runtime.PublicURL, "/") + "/portal/verify-email?token=" + token
	site := runtime.AppName
	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		subject := site + " 邮箱验证"
		body := "您好，\n\n感谢注册 " + site + "。请在 24 小时内打开下面的链接验证邮箱：\n\n" + link +
			"\n\n验证后才能下单、充值和接收通知。如果不是您本人注册，请忽略这封邮件。\n"
		if lang == "en" {
			subject = site + ": verify your email address"
			body = "Hello,\n\nThank you for signing up at " + site + ". Open the link below within 24 hours to verify your email address:\n\n" + link +
				"\n\nYou can order, top up and receive notifications once it is verified. If you did not sign up, please ignore this email.\n"
		}
		if err := mail.Send(sendCtx, runtime.SMTP, email, subject, body); err != nil {
			slog.Default().Warn("send verification mail", "error", err)
		}
	}()
	return nil
}

func (a *authenticator) customerResendVerification(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	if identity.EmailVerified {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"email_verified": true}})
		return
	}
	if !a.settings.Current().SMTP.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "mail_unavailable", "message": "站点暂未配置邮件，请联系客服"})
		return
	}
	if count, err := a.store.RecentEmailVerifications(r.Context(), identity.UserID); err != nil || count >= emailVerifyPerHour {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "message": "发送过于频繁，请稍后再试"})
		return
	}
	if err := a.sendVerification(r.Context(), identity.UserID, identity.Email, a.mailLang(r, identity.Locale)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"message": "验证邮件已发送到 " + identity.Email}})
}

func (a *authenticator) customerConfirmEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_token", "message": "验证链接无效"})
		return
	}
	userID, err := a.store.ConfirmEmail(r.Context(), security.HashToken(token))
	if errors.Is(err, postgres.ErrResetTokenInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_token", "message": "验证链接无效或已过期，请登录后重新发送"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), userID, "customer", "email.verified", remoteIP(r), r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"email_verified": true}})
}

// requireVerifiedCustomer guards actions that spend money or reach other
// people: the customer must have confirmed their email address.
func (a *authenticator) requireVerifiedCustomer(next http.Handler) http.Handler {
	return a.requireCustomer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !customerPrincipalFromContext(r.Context()).EmailVerified {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "email_unverified", "message": "请先验证邮箱：打开注册时收到的验证邮件，或在页面顶部重新发送"})
			return
		}
		next.ServeHTTP(w, r)
	}))
}
