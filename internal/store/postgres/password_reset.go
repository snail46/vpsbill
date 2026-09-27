package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrResetTokenInvalid covers unknown, used and expired reset links alike so
// callers cannot tell them apart.
var ErrResetTokenInvalid = errors.New("password reset link is invalid or expired")

// ErrCustomerNotFound is returned when an account has no active login.
var ErrCustomerNotFound = errors.New("customer login not found")

// CreatePasswordReset stores a reset link for a user. Older unused links for
// the same user stop working, so only the newest one is valid.
func (s *AuthStore) CreatePasswordReset(ctx context.Context, userID string, tokenHash []byte, createdBy string, expiresAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO password_reset_tokens(user_id,token_hash,created_by,expires_at) VALUES($1,$2,$3,$4)`, userID, tokenHash, createdBy, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecentPasswordResets counts links a user requested for themselves in the
// last hour, to throttle reset mail.
func (s *AuthStore) RecentPasswordResets(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND created_by='self' AND created_at>now()-interval '1 hour'`, userID).Scan(&count)
	return count, err
}

// ResetPassword consumes a reset link, sets the new password and signs the
// user out everywhere.
func (s *AuthStore) ResetPassword(ctx context.Context, tokenHash []byte, passwordHash string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE password_reset_tokens SET used_at=now()
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now()
		RETURNING user_id
	`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrResetTokenInvalid
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, userID, passwordHash); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM login_sessions WHERE user_id=$1`, userID); err != nil {
		return "", err
	}
	return userID, tx.Commit(ctx)
}

// AccountOwnerLogin returns the login that owns a customer account.
func (s *AuthStore) AccountOwnerLogin(ctx context.Context, accountID string) (userID, email string, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT u.id,u.email FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.account_id=$1 AND u.status='active'
		ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,m.created_at LIMIT 1
	`, accountID).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrCustomerNotFound
	}
	return userID, email, err
}
