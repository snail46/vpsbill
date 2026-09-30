package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrReinstallRunning is returned when a service is already being
// reinstalled.
var ErrReinstallRunning = errors.New("reinstall already running")

// staleReinstall is how long a reinstall may stay running before it counts
// as failed: the API gives each one 15 minutes, and one left running by a
// stopped API process is released this way.
const staleReinstall = 20 * time.Minute

// Reinstall is the latest reinstall of a service, which runs in the
// background while the customer's page follows it.
type Reinstall struct {
	TemplateID string     `json:"template_id"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// StartReinstall records a reinstall as running, unless one already is.
func (s *PortalStore) StartReinstall(ctx context.Context, accountID, serviceID, templateID string) (Reinstall, error) {
	var result Reinstall
	err := s.db.QueryRow(ctx, `
		INSERT INTO service_reinstalls(service_id,template_id,status,error,started_at,finished_at)
		SELECT id,$3,'running','',now(),NULL FROM services WHERE id=$1 AND account_id=$2
		ON CONFLICT (service_id) DO UPDATE SET template_id=EXCLUDED.template_id,status='running',error='',started_at=now(),finished_at=NULL
		WHERE service_reinstalls.status<>'running' OR service_reinstalls.started_at < now()-make_interval(secs => $4)
		RETURNING template_id,status,started_at
	`, serviceID, accountID, templateID, staleReinstall.Seconds()).Scan(&result.TemplateID, &result.Status, &result.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM services WHERE id=$1 AND account_id=$2)`, serviceID, accountID).Scan(&exists); err != nil {
			return Reinstall{}, err
		}
		if !exists {
			return Reinstall{}, ErrServiceNotFound
		}
		return Reinstall{}, ErrReinstallRunning
	}
	return result, err
}

// FinishReinstall records how a reinstall ended; failure is empty on
// success.
func (s *PortalStore) FinishReinstall(ctx context.Context, serviceID, failure string) error {
	status := "succeeded"
	if failure != "" {
		status = "failed"
	}
	_, err := s.db.Exec(ctx, `UPDATE service_reinstalls SET status=$2,error=$3,finished_at=now() WHERE service_id=$1 AND status='running'`, serviceID, status, failure)
	return err
}

// LatestReinstall returns the service's latest reinstall, or nil.
func (s *PortalStore) LatestReinstall(ctx context.Context, accountID, serviceID string) (*Reinstall, error) {
	var result Reinstall
	err := s.db.QueryRow(ctx, `
		SELECT r.template_id,
		       CASE WHEN r.status='running' AND r.started_at < now()-make_interval(secs => $3) THEN 'failed' ELSE r.status END,
		       CASE WHEN r.status='running' AND r.started_at < now()-make_interval(secs => $3) THEN '重装超时，请查看实例状态或联系客服' ELSE r.error END,
		       r.started_at,r.finished_at
		FROM service_reinstalls r JOIN services s ON s.id=r.service_id
		WHERE r.service_id=$1 AND s.account_id=$2
	`, serviceID, accountID, staleReinstall.Seconds()).Scan(&result.TemplateID, &result.Status, &result.Error, &result.StartedAt, &result.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}
