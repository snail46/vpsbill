package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrBootstrapComplete = errors.New("administrator bootstrap is already complete")
	ErrInvalidLogin      = errors.New("invalid email or password")
	ErrEmailExists       = errors.New("email already exists")
)

type StaffIdentity struct {
	UserID             string   `json:"id"`
	Email              string   `json:"email"`
	DisplayName        string   `json:"display_name"`
	PasswordHash       string   `json:"-"`
	Status             string   `json:"-"`
	RoleName           string   `json:"role"`
	Permissions        []string `json:"permissions"`
	MFAEnabled         bool     `json:"mfa_enabled"`
	MFASecretEncrypted []byte   `json:"-"`
}

type SessionIdentity struct {
	StaffIdentity
	CSRFHash  []byte
	ExpiresAt time.Time
}

type CustomerIdentity struct {
	UserID             string `json:"id"`
	AccountID          string `json:"account_id"`
	Email              string `json:"email"`
	DisplayName        string `json:"display_name"`
	PasswordHash       string `json:"-"`
	Status             string `json:"-"`
	AccountStatus      string `json:"account_status"`
	DefaultCurrency    string `json:"default_currency"`
	Role               string `json:"role"`
	MFAEnabled         bool   `json:"mfa_enabled"`
	MFASecretEncrypted []byte `json:"-"`
}

type CustomerSessionIdentity struct {
	CustomerIdentity
	CSRFHash  []byte
	ExpiresAt time.Time
}

type AuthStore struct {
	db *pgxpool.Pool
}

func NewAuthStore(db *pgxpool.Pool) *AuthStore {
	return &AuthStore{db: db}
}

func (s *AuthStore) BootstrapRequired(ctx context.Context) (bool, error) {
	var required bool
	err := s.db.QueryRow(ctx, "SELECT NOT EXISTS (SELECT 1 FROM staff_members)").Scan(&required)
	return required, err
}

func (s *AuthStore) BootstrapAdmin(ctx context.Context, email, displayName, passwordHash string) (StaffIdentity, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return StaffIdentity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(74210921)"); err != nil {
		return StaffIdentity{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM staff_members").Scan(&count); err != nil {
		return StaffIdentity{}, err
	}
	if count != 0 {
		return StaffIdentity{}, ErrBootstrapComplete
	}

	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users(email, display_name, password_hash, email_verified_at, status)
		VALUES($1, $2, $3, now(), 'active')
		RETURNING id
	`, email, displayName, passwordHash).Scan(&userID); err != nil {
		return StaffIdentity{}, fmt.Errorf("create administrator: %w", err)
	}
	var roleID string
	var roleName string
	var permissions []string
	if err := tx.QueryRow(ctx, `
		SELECT id, name, permissions FROM staff_roles WHERE name='Super Administrator'
	`).Scan(&roleID, &roleName, &permissions); err != nil {
		return StaffIdentity{}, fmt.Errorf("load administrator role: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO staff_members(user_id, role_id) VALUES($1, $2)", userID, roleID); err != nil {
		return StaffIdentity{}, fmt.Errorf("grant administrator role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffIdentity{}, err
	}
	return StaffIdentity{
		UserID:      userID,
		Email:       email,
		DisplayName: displayName,
		Status:      "active",
		RoleName:    roleName,
		Permissions: permissions,
	}, nil
}

func (s *AuthStore) StaffByEmail(ctx context.Context, email string) (StaffIdentity, error) {
	var identity StaffIdentity
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.password_hash, u.status, r.name, r.permissions,u.mfa_enabled,u.mfa_secret_encrypted
		FROM users u
		JOIN staff_members sm ON sm.user_id=u.id
		JOIN staff_roles r ON r.id=sm.role_id
		WHERE lower(u.email)=lower($1)
	`, strings.TrimSpace(email)).Scan(
		&identity.UserID,
		&identity.Email,
		&identity.DisplayName,
		&identity.PasswordHash,
		&identity.Status,
		&identity.RoleName,
		&identity.Permissions,
		&identity.MFAEnabled,
		&identity.MFASecretEncrypted,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffIdentity{}, ErrInvalidLogin
	}
	return identity, err
}

func (s *AuthStore) CreateSession(ctx context.Context, userID string, tokenHash, csrfHash []byte, expiresAt time.Time, ip, userAgent string) error {
	var ipValue any
	if strings.TrimSpace(ip) != "" {
		ipValue = strings.TrimSpace(ip)
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO login_sessions(user_id, token_hash, csrf_hash, expires_at, ip, user_agent)
		VALUES($1, $2, $3, $4, $5, $6)
	`, userID, tokenHash, csrfHash, expiresAt, ipValue, userAgent)
	return err
}

func (s *AuthStore) SessionByToken(ctx context.Context, tokenHash []byte) (SessionIdentity, error) {
	var identity SessionIdentity
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.status, r.name, r.permissions,u.mfa_enabled,
		       s.csrf_hash, s.expires_at
		FROM login_sessions s
		JOIN users u ON u.id=s.user_id
		JOIN staff_members sm ON sm.user_id=u.id
		JOIN staff_roles r ON r.id=sm.role_id
		WHERE s.token_hash=$1 AND s.expires_at > now() AND u.status='active'
	`, tokenHash).Scan(
		&identity.UserID,
		&identity.Email,
		&identity.DisplayName,
		&identity.Status,
		&identity.RoleName,
		&identity.Permissions,
		&identity.MFAEnabled,
		&identity.CSRFHash,
		&identity.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionIdentity{}, ErrInvalidLogin
	}
	return identity, err
}

func (s *AuthStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.Exec(ctx, "DELETE FROM login_sessions WHERE token_hash=$1", tokenHash)
	return err
}

func (s *AuthStore) RegisterCustomer(ctx context.Context, email, displayName, passwordHash string) (CustomerIdentity, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return CustomerIdentity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	var identity CustomerIdentity
	err = tx.QueryRow(ctx, `
		INSERT INTO users(email,display_name,password_hash,status)
		VALUES($1,$2,$3,'active') RETURNING id,email,display_name,status
	`, email, displayName, passwordHash).Scan(&identity.UserID, &identity.Email, &identity.DisplayName, &identity.Status)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return CustomerIdentity{}, ErrEmailExists
		}
		return CustomerIdentity{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO accounts(kind,status,display_name,billing_email,country_code,default_currency)
		VALUES('individual','active',$1,$2,'CN','CNY') RETURNING id,status
	`, displayName, email).Scan(&identity.AccountID, &identity.AccountStatus)
	if err != nil {
		return CustomerIdentity{}, err
	}
	identity.Role = "owner"
	identity.DefaultCurrency = "CNY"
	if _, err := tx.Exec(ctx, "INSERT INTO memberships(account_id,user_id,role) VALUES($1,$2,'owner')", identity.AccountID, identity.UserID); err != nil {
		return CustomerIdentity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomerIdentity{}, err
	}
	return identity, nil
}

func (s *AuthStore) CustomerByEmail(ctx context.Context, email string) (CustomerIdentity, error) {
	var identity CustomerIdentity
	err := s.db.QueryRow(ctx, `
		SELECT u.id,m.account_id,u.email,u.display_name,u.password_hash,u.status,a.status,a.default_currency,m.role,u.mfa_enabled,u.mfa_secret_encrypted
		FROM users u JOIN memberships m ON m.user_id=u.id JOIN accounts a ON a.id=m.account_id
		WHERE lower(u.email)=lower($1)
		ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,m.created_at LIMIT 1
	`, strings.TrimSpace(email)).Scan(&identity.UserID, &identity.AccountID, &identity.Email, &identity.DisplayName, &identity.PasswordHash, &identity.Status, &identity.AccountStatus, &identity.DefaultCurrency, &identity.Role, &identity.MFAEnabled, &identity.MFASecretEncrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerIdentity{}, ErrInvalidLogin
	}
	return identity, err
}

func (s *AuthStore) CustomerSessionByToken(ctx context.Context, tokenHash []byte) (CustomerSessionIdentity, error) {
	var identity CustomerSessionIdentity
	err := s.db.QueryRow(ctx, `
		SELECT u.id,m.account_id,u.email,u.display_name,u.status,a.status,a.default_currency,m.role,u.mfa_enabled,s.csrf_hash,s.expires_at
		FROM login_sessions s JOIN users u ON u.id=s.user_id JOIN memberships m ON m.user_id=u.id JOIN accounts a ON a.id=m.account_id
		WHERE s.token_hash=$1 AND s.expires_at>now() AND u.status='active' AND a.status='active'
		ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,m.created_at LIMIT 1
	`, tokenHash).Scan(&identity.UserID, &identity.AccountID, &identity.Email, &identity.DisplayName, &identity.Status, &identity.AccountStatus, &identity.DefaultCurrency, &identity.Role, &identity.MFAEnabled, &identity.CSRFHash, &identity.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerSessionIdentity{}, ErrInvalidLogin
	}
	return identity, err
}

func (s *AuthStore) LoginAllowed(ctx context.Context, email, ip string) (bool, error) {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	var emailFailures, ipFailures int
	err := s.db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE email_hash=$1),count(*) FILTER(WHERE $2<>'' AND ip=nullif($2,'')::inet) FROM login_attempts WHERE successful=false AND created_at>now()-interval '15 minutes'`, hash[:], ip).Scan(&emailFailures, &ipFailures)
	return emailFailures < 10 && ipFailures < 50, err
}

func (s *AuthStore) RecordLoginAttempt(ctx context.Context, email, ip string, successful bool) error {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	if successful {
		_, err := s.db.Exec(ctx, `WITH cleared AS (DELETE FROM login_attempts WHERE email_hash=$1 AND successful=false) INSERT INTO login_attempts(email_hash,ip,successful) VALUES($1,nullif($2,'')::inet,true)`, hash[:], ip)
		return err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO login_attempts(email_hash,ip,successful) VALUES($1,nullif($2,'')::inet,false)`, hash[:], ip)
	return err
}

func (s *AuthStore) SetPendingMFA(ctx context.Context, userID string, encrypted []byte) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET mfa_pending_secret_encrypted=$2 WHERE id=$1`, userID, encrypted)
	return err
}
func (s *AuthStore) PendingMFA(ctx context.Context, userID string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRow(ctx, `SELECT mfa_pending_secret_encrypted FROM users WHERE id=$1`, userID).Scan(&value)
	return value, err
}
func (s *AuthStore) EnableMFA(ctx context.Context, userID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE users SET mfa_enabled=true,mfa_secret_encrypted=mfa_pending_secret_encrypted,mfa_pending_secret_encrypted=NULL WHERE id=$1 AND mfa_pending_secret_encrypted IS NOT NULL`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("mfa setup not started")
	}
	return nil
}
func (s *AuthStore) DisableMFA(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET mfa_enabled=false,mfa_secret_encrypted=NULL,mfa_pending_secret_encrypted=NULL WHERE id=$1`, userID)
	return err
}
func (s *AuthStore) MFASecret(ctx context.Context, userID string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRow(ctx, `SELECT mfa_secret_encrypted FROM users WHERE id=$1 AND mfa_enabled=true`, userID).Scan(&value)
	return value, err
}
func (s *AuthStore) WriteSecurityAudit(ctx context.Context, userID, actorType, action, ip, userAgent string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,ip,user_agent) VALUES($2,$1,$3,'user',$1,nullif($4,'')::inet,$5)`, userID, actorType, action, ip, userAgent)
	return err
}

// PasswordHash returns a user's current password hash.
func (s *AuthStore) PasswordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	err := s.db.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash)
	return hash, err
}

// ChangePassword replaces the password and signs out every other session,
// keeping only the one identified by keepTokenHash.
func (s *AuthStore) ChangePassword(ctx context.Context, userID, passwordHash string, keepTokenHash []byte) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, userID, passwordHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM login_sessions WHERE user_id=$1 AND token_hash<>$2`, userID, keepTokenHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
