package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SuperAdministrator is the role that may do everything, including
// managing the other staff.
const SuperAdministrator = "Super Administrator"

var (
	ErrStaffNotFound   = errors.New("staff member not found")
	ErrRoleNotFound    = errors.New("staff role not found")
	ErrStaffSelf       = errors.New("staff cannot change their own role or access")
	ErrLastSuperAdmin  = errors.New("the last super administrator must stay")
	ErrEmailIsCustomer = errors.New("the address belongs to a customer")
)

// StaffRole is one of the fixed roles; a lower level is a higher rank.
type StaffRole struct {
	Name        string   `json:"name"`
	Level       int      `json:"level"`
	Permissions []string `json:"permissions"`
	Members     int      `json:"members"`
}

type StaffMember struct {
	UserID      string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	Role        string    `json:"role"`
	RoleLevel   int       `json:"role_level"`
	MFAEnabled  bool      `json:"mfa_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	// LastLoginAt is the newest sign-in still on record.
	LastLoginAt *time.Time `json:"last_login_at"`
}

func (s *AuthStore) StaffRoles(ctx context.Context) ([]StaffRole, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.name,r.level,r.permissions,(SELECT count(*) FROM staff_members sm WHERE sm.role_id=r.id)
		FROM staff_roles r ORDER BY r.level,r.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StaffRole, 0)
	for rows.Next() {
		var row StaffRole
		if err := rows.Scan(&row.Name, &row.Level, &row.Permissions, &row.Members); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

const staffMemberSelect = `
	SELECT u.id,u.email,u.display_name,u.status,r.name,r.level,u.mfa_enabled,sm.created_at,
	       (SELECT max(ls.created_at) FROM login_sessions ls WHERE ls.user_id=u.id)
	FROM staff_members sm JOIN users u ON u.id=sm.user_id JOIN staff_roles r ON r.id=sm.role_id`

func scanStaffMember(row pgx.Row) (StaffMember, error) {
	var member StaffMember
	err := row.Scan(&member.UserID, &member.Email, &member.DisplayName, &member.Status, &member.Role, &member.RoleLevel, &member.MFAEnabled, &member.CreatedAt, &member.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffMember{}, ErrStaffNotFound
	}
	return member, err
}

func (s *AuthStore) StaffMembers(ctx context.Context) ([]StaffMember, error) {
	rows, err := s.db.Query(ctx, staffMemberSelect+` ORDER BY r.level,sm.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StaffMember, 0)
	for rows.Next() {
		member, err := scanStaffMember(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, member)
	}
	return result, rows.Err()
}

// staffTx serializes changes to the staff list, so the check that a super
// administrator remains cannot race.
func (s *AuthStore) staffTx(ctx context.Context, change func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(74210922)"); err != nil {
		return err
	}
	if err := change(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func staffAudit(ctx context.Context, tx pgx.Tx, actorID, action, userID string, metadata map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,$2,'user',$3,$4)`, actorID, action, userID, metadata)
	return err
}

func roleID(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM staff_roles WHERE name=$1`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRoleNotFound
	}
	return id, err
}

// CreateStaff adds a staff member with a role. The address must be new, or
// that of a staff member removed earlier.
func (s *AuthStore) CreateStaff(ctx context.Context, actorID, email, displayName, passwordHash, role string) (StaffMember, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	var userID string
	err := s.staffTx(ctx, func(tx pgx.Tx) error {
		roleKey, err := roleID(ctx, tx, role)
		if err != nil {
			return err
		}
		var isStaff, isCustomer bool
		err = tx.QueryRow(ctx, `
			SELECT u.id,EXISTS(SELECT 1 FROM staff_members sm WHERE sm.user_id=u.id),EXISTS(SELECT 1 FROM memberships m WHERE m.user_id=u.id)
			FROM users u WHERE lower(u.email)=$1
		`, email).Scan(&userID, &isStaff, &isCustomer)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if err := tx.QueryRow(ctx, `
				INSERT INTO users(email,display_name,password_hash,email_verified_at,status) VALUES($1,$2,$3,now(),'active') RETURNING id
			`, email, displayName, passwordHash).Scan(&userID); err != nil {
				return err
			}
		case err != nil:
			return err
		case isStaff:
			return ErrEmailExists
		case isCustomer:
			return ErrEmailIsCustomer
		default:
			// A removed staff member comes back with a new password and
			// without the old second factor.
			if _, err := tx.Exec(ctx, `
				UPDATE users SET display_name=$2,password_hash=$3,status='active',mfa_enabled=false,mfa_secret_encrypted=NULL,
				       mfa_pending_secret_encrypted=NULL,email_verified_at=coalesce(email_verified_at,now()),updated_at=now()
				WHERE id=$1
			`, userID, displayName, passwordHash); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_members(user_id,role_id) VALUES($1,$2)`, userID, roleKey); err != nil {
			return err
		}
		return staffAudit(ctx, tx, actorID, "staff.created", userID, map[string]any{"email": email, "role": role})
	})
	if err != nil {
		return StaffMember{}, err
	}
	return scanStaffMember(s.db.QueryRow(ctx, staffMemberSelect+` WHERE u.id=$1`, userID))
}

// keepSuperAdmin fails when userID is the only active super administrator.
func keepSuperAdmin(ctx context.Context, tx pgx.Tx, userID string) error {
	var isSuper bool
	var others int
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM staff_members sm JOIN staff_roles r ON r.id=sm.role_id JOIN users u ON u.id=sm.user_id
		              WHERE sm.user_id=$1 AND r.name=$2 AND u.status='active'),
		       (SELECT count(*) FROM staff_members sm JOIN staff_roles r ON r.id=sm.role_id JOIN users u ON u.id=sm.user_id
		        WHERE sm.user_id<>$1 AND r.name=$2 AND u.status='active')
	`, userID, SuperAdministrator).Scan(&isSuper, &others)
	if err != nil {
		return err
	}
	if isSuper && others == 0 {
		return ErrLastSuperAdmin
	}
	return nil
}

// StaffChange is what UpdateStaff may change; nil keeps a value.
type StaffChange struct {
	Role        *string
	Status      *string
	DisplayName *string
}

// UpdateStaff changes a staff member's role, access or name. Nobody
// changes their own role or access, and one active super administrator
// always remains.
func (s *AuthStore) UpdateStaff(ctx context.Context, actorID, userID string, change StaffChange) (StaffMember, error) {
	err := s.staffTx(ctx, func(tx pgx.Tx) error {
		var currentRole, currentStatus string
		err := tx.QueryRow(ctx, `
			SELECT r.name,u.status FROM staff_members sm JOIN staff_roles r ON r.id=sm.role_id JOIN users u ON u.id=sm.user_id WHERE sm.user_id=$1
		`, userID).Scan(&currentRole, &currentStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		if err != nil {
			return err
		}
		metadata := map[string]any{}
		roleChanges := change.Role != nil && *change.Role != currentRole
		statusChanges := change.Status != nil && *change.Status != currentStatus
		if (roleChanges || statusChanges) && actorID == userID {
			return ErrStaffSelf
		}
		if roleChanges || (statusChanges && *change.Status != "active") {
			if err := keepSuperAdmin(ctx, tx, userID); err != nil {
				return err
			}
		}
		if roleChanges {
			role, err := roleID(ctx, tx, *change.Role)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE staff_members SET role_id=$2 WHERE user_id=$1`, userID, role); err != nil {
				return err
			}
			metadata["role"] = *change.Role
		}
		if statusChanges {
			if _, err := tx.Exec(ctx, `UPDATE users SET status=$2,updated_at=now() WHERE id=$1`, userID, *change.Status); err != nil {
				return err
			}
			if *change.Status != "active" {
				if _, err := tx.Exec(ctx, `DELETE FROM login_sessions WHERE user_id=$1`, userID); err != nil {
					return err
				}
			}
			metadata["status"] = *change.Status
		}
		if change.DisplayName != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET display_name=$2,updated_at=now() WHERE id=$1`, userID, strings.TrimSpace(*change.DisplayName)); err != nil {
				return err
			}
			metadata["display_name"] = strings.TrimSpace(*change.DisplayName)
		}
		return staffAudit(ctx, tx, actorID, "staff.updated", userID, metadata)
	})
	if err != nil {
		return StaffMember{}, err
	}
	return scanStaffMember(s.db.QueryRow(ctx, staffMemberSelect+` WHERE u.id=$1`, userID))
}

// ResetStaffPassword gives a staff member a new password and signs them
// out; resetMFA also removes their second factor, for a lost phone.
func (s *AuthStore) ResetStaffPassword(ctx context.Context, actorID, userID, passwordHash string, resetMFA bool) error {
	return s.staffTx(ctx, func(tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `
			UPDATE users u SET password_hash=$2,updated_at=now(),
			       mfa_enabled=CASE WHEN $3 THEN false ELSE mfa_enabled END,
			       mfa_secret_encrypted=CASE WHEN $3 THEN NULL ELSE mfa_secret_encrypted END,
			       mfa_pending_secret_encrypted=CASE WHEN $3 THEN NULL ELSE mfa_pending_secret_encrypted END
			WHERE u.id=$1 AND EXISTS(SELECT 1 FROM staff_members sm WHERE sm.user_id=u.id)
		`, userID, passwordHash, resetMFA)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			return ErrStaffNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM login_sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
		return staffAudit(ctx, tx, actorID, "staff.password_reset", userID, map[string]any{"mfa_reset": resetMFA})
	})
}

// RemoveStaff takes a member off the staff. Their user record stays for
// the history it is named in, disabled unless it is also a customer.
func (s *AuthStore) RemoveStaff(ctx context.Context, actorID, userID string) error {
	if actorID == userID {
		return ErrStaffSelf
	}
	return s.staffTx(ctx, func(tx pgx.Tx) error {
		if err := keepSuperAdmin(ctx, tx, userID); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `DELETE FROM staff_members WHERE user_id=$1`, userID)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			return ErrStaffNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM login_sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users u SET status='disabled',updated_at=now() WHERE u.id=$1 AND NOT EXISTS(SELECT 1 FROM memberships m WHERE m.user_id=u.id)`, userID); err != nil {
			return err
		}
		return staffAudit(ctx, tx, actorID, "staff.removed", userID, map[string]any{})
	})
}
