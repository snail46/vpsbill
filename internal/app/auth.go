package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"clicd-billing/internal/config"
	"clicd-billing/internal/security"
	"clicd-billing/internal/settings"
	"clicd-billing/internal/store/postgres"
)

const (
	sessionCookieName = "cb_session"
	csrfCookieName    = "cb_csrf"
	sessionDuration   = 12 * time.Hour
)

type principalContextKey struct{}
type customerPrincipalContextKey struct{}

type authenticator struct {
	cfg      config.Config
	store    *postgres.AuthStore
	box      *security.SecretBox
	settings *settings.Manager
}

func newAuthenticator(cfg config.Config, store *postgres.AuthStore, box *security.SecretBox, runtime *settings.Manager) *authenticator {
	return &authenticator{cfg: cfg, store: store, box: box, settings: runtime}
}

func (a *authenticator) bootstrapStatus(w http.ResponseWriter, r *http.Request) {
	required, err := a.store.BootstrapRequired(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"required": required}})
}

func (a *authenticator) bootstrap(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Password    string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" || !validEmail(input.Email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "管理员姓名和有效邮箱不能为空"})
		return
	}
	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	identity, err := a.store.BootstrapAdmin(r.Context(), input.Email, input.DisplayName, passwordHash)
	if errors.Is(err, postgres.ErrBootstrapComplete) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "bootstrap_complete", "message": "初始管理员已经创建"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	a.startSession(w, r, identity)
}

func (a *authenticator) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if allowed, err := a.store.LoginAllowed(r.Context(), input.Email, remoteIP(r)); err != nil || !allowed {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "login_rate_limited", "message": "登录尝试过多，请稍后再试"})
		return
	}
	identity, err := a.store.StaffByEmail(r.Context(), input.Email)
	if err != nil || identity.Status != "active" {
		_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), false)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_credentials", "message": "邮箱或密码错误"})
		return
	}
	valid, err := security.VerifyPassword(identity.PasswordHash, input.Password)
	if err != nil || !valid {
		_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), false)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_credentials", "message": "邮箱或密码错误"})
		return
	}
	if identity.MFAEnabled && !a.verifyEncryptedTOTP(identity.MFASecretEncrypted, input.TOTPCode) {
		_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), false)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "mfa_required", "message": "需要有效的二步验证码"})
		return
	}
	_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), true)
	a.startSession(w, r, identity)
}

func (a *authenticator) startSession(w http.ResponseWriter, r *http.Request, identity postgres.StaffIdentity) {
	a.issueSession(w, r, identity.UserID, identity)
}

func (a *authenticator) issueSession(w http.ResponseWriter, r *http.Request, userID string, response any) {
	sessionToken, sessionHash, err := security.NewToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	csrfToken, csrfHash, err := security.NewToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	expiresAt := time.Now().Add(sessionDuration)
	if err := a.store.CreateSession(r.Context(), userID, sessionHash, csrfHash, expiresAt, remoteIP(r), r.UserAgent()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	a.setCookie(w, sessionCookieName, sessionToken, expiresAt, true)
	a.setCookie(w, csrfCookieName, csrfToken, expiresAt, false)
	writeJSON(w, http.StatusOK, map[string]any{"data": response})
}

func (a *authenticator) customerRegister(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Password    string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" || !validEmail(input.Email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "姓名和有效邮箱不能为空"})
		return
	}
	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	identity, err := a.store.RegisterCustomer(r.Context(), input.Email, input.DisplayName, passwordHash)
	if errors.Is(err, postgres.ErrEmailExists) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "email_exists", "message": "该邮箱已被注册"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	a.issueSession(w, r, identity.UserID, identity)
}

func (a *authenticator) customerLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if allowed, err := a.store.LoginAllowed(r.Context(), input.Email, remoteIP(r)); err != nil || !allowed {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "login_rate_limited", "message": "登录尝试过多，请稍后再试"})
		return
	}
	identity, err := a.store.CustomerByEmail(r.Context(), input.Email)
	if err != nil || identity.Status != "active" || identity.AccountStatus != "active" {
		_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), false)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_credentials", "message": "邮箱或密码错误"})
		return
	}
	valid, err := security.VerifyPassword(identity.PasswordHash, input.Password)
	if err != nil || !valid {
		_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), false)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_credentials", "message": "邮箱或密码错误"})
		return
	}
	if identity.MFAEnabled && !a.verifyEncryptedTOTP(identity.MFASecretEncrypted, input.TOTPCode) {
		_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), false)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "mfa_required", "message": "需要有效的二步验证码"})
		return
	}
	_ = a.store.RecordLoginAttempt(r.Context(), input.Email, remoteIP(r), true)
	a.issueSession(w, r, identity.UserID, identity)
}

func (a *authenticator) verifyEncryptedTOTP(encrypted []byte, code string) bool {
	secret, err := a.box.Open(encrypted)
	return err == nil && security.VerifyTOTP(secret, code, time.Now().UTC())
}

func (a *authenticator) mfaSetup(w http.ResponseWriter, r *http.Request, userID, email, actorType string) {
	secret, err := security.NewTOTPSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	encrypted, err := a.box.Seal(secret)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if err = a.store.SetPendingMFA(r.Context(), userID, encrypted); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), userID, actorType, "mfa.setup_started", remoteIP(r), r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"secret": secret, "otpauth_uri": security.TOTPUri(a.settings.Current().AppName, email, secret)}})
}
func (a *authenticator) mfaConfirm(w http.ResponseWriter, r *http.Request, userID, actorType string) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	encrypted, err := a.store.PendingMFA(r.Context(), userID)
	if err != nil || !a.verifyEncryptedTOTP(encrypted, input.Code) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_totp", "message": "验证码无效"})
		return
	}
	if err = a.store.EnableMFA(r.Context(), userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), userID, actorType, "mfa.enabled", remoteIP(r), r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"mfa_enabled": true}})
}
func (a *authenticator) mfaDisable(w http.ResponseWriter, r *http.Request, userID, actorType string) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	encrypted, err := a.store.MFASecret(r.Context(), userID)
	if err != nil || !a.verifyEncryptedTOTP(encrypted, input.Code) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_totp", "message": "验证码无效"})
		return
	}
	if err = a.store.DisableMFA(r.Context(), userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	_ = a.store.WriteSecurityAudit(r.Context(), userID, actorType, "mfa.disabled", remoteIP(r), r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"mfa_enabled": false}})
}
func (a *authenticator) staffMFASetup(w http.ResponseWriter, r *http.Request) {
	i := principalFromContext(r.Context())
	a.mfaSetup(w, r, i.UserID, i.Email, "staff")
}
func (a *authenticator) staffMFAConfirm(w http.ResponseWriter, r *http.Request) {
	i := principalFromContext(r.Context())
	a.mfaConfirm(w, r, i.UserID, "staff")
}
func (a *authenticator) staffMFADisable(w http.ResponseWriter, r *http.Request) {
	i := principalFromContext(r.Context())
	a.mfaDisable(w, r, i.UserID, "staff")
}
func (a *authenticator) customerMFASetup(w http.ResponseWriter, r *http.Request) {
	i := customerPrincipalFromContext(r.Context())
	a.mfaSetup(w, r, i.UserID, i.Email, "customer")
}
func (a *authenticator) customerMFAConfirm(w http.ResponseWriter, r *http.Request) {
	i := customerPrincipalFromContext(r.Context())
	a.mfaConfirm(w, r, i.UserID, "customer")
}
func (a *authenticator) customerMFADisable(w http.ResponseWriter, r *http.Request) {
	i := customerPrincipalFromContext(r.Context())
	a.mfaDisable(w, r, i.UserID, "customer")
}

func (a *authenticator) customerMe(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"data": identity.CustomerIdentity})
}

func (a *authenticator) me(w http.ResponseWriter, r *http.Request) {
	identity := principalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"data": identity.StaffIdentity})
}

func (a *authenticator) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookieName)
	if cookie != nil {
		_ = a.store.DeleteSession(r.Context(), security.HashToken(cookie.Value))
	}
	a.clearCookie(w, sessionCookieName, true)
	a.clearCookie(w, csrfCookieName, false)
	w.WriteHeader(http.StatusNoContent)
}

func (a *authenticator) require(permission string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication_required"})
			return
		}
		identity, err := a.store.SessionByToken(r.Context(), security.HashToken(cookie.Value))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication_required"})
			return
		}
		if permission != "" && !hasPermission(identity.Permissions, permission) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "permission_denied"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			csrfCookie, cookieErr := r.Cookie(csrfCookieName)
			header := r.Header.Get("X-CSRF-Token")
			if cookieErr != nil || header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(csrfCookie.Value)) != 1 || subtle.ConstantTimeCompare(security.HashToken(header), identity.CSRFHash) != 1 {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "csrf_validation_failed"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *authenticator) requireCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication_required"})
			return
		}
		identity, err := a.store.CustomerSessionByToken(r.Context(), security.HashToken(cookie.Value))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication_required"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			csrfCookie, cookieErr := r.Cookie(csrfCookieName)
			header := r.Header.Get("X-CSRF-Token")
			if cookieErr != nil || header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(csrfCookie.Value)) != 1 || subtle.ConstantTimeCompare(security.HashToken(header), identity.CSRFHash) != 1 {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "csrf_validation_failed"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), customerPrincipalContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func principalFromContext(ctx context.Context) postgres.SessionIdentity {
	identity, _ := ctx.Value(principalContextKey{}).(postgres.SessionIdentity)
	return identity
}

func customerPrincipalFromContext(ctx context.Context) postgres.CustomerSessionIdentity {
	identity, _ := ctx.Value(customerPrincipalContextKey{}).(postgres.CustomerSessionIdentity)
	return identity
}

func hasPermission(permissions []string, required string) bool {
	for _, permission := range permissions {
		if permission == "*" || permission == required {
			return true
		}
	}
	return false
}

func (a *authenticator) setCookie(w http.ResponseWriter, name, value string, expires time.Time, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: httpOnly,
		Secure:   a.cfg.Environment == "production" && strings.HasPrefix(a.settings.Current().PublicURL, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *authenticator) clearCookie(w http.ResponseWriter, name string, httpOnly bool) {
	a.setCookie(w, name, "", time.Unix(1, 0), httpOnly)
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(address.Address, strings.TrimSpace(value))
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		peer := net.ParseIP(host)
		if peer != nil && (peer.IsPrivate() || peer.IsLoopback()) {
			if chain := strings.Split(r.Header.Get("X-Forwarded-For"), ","); len(chain) > 0 {
				if forwarded := net.ParseIP(strings.TrimSpace(chain[0])); forwarded != nil {
					return forwarded.String()
				}
			}
			if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); forwarded != nil {
				return forwarded.String()
			}
		}
		return host
	}
	return ""
}
