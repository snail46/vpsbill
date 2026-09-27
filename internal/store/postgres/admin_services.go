package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"vpsbill/internal/security"
)

// QueueAdminServiceAction lets staff drive a service outside the billing
// lifecycle: power actions on running services, and immediate termination
// (for abuse or refunds) of anything not yet terminated.
func (s *ProvisioningStore) QueueAdminServiceAction(ctx context.Context, staffID, serviceID, action, ip, userAgent string) (string, error) {
	switch action {
	case "start", "stop", "restart", "terminate":
	default:
		return "", ErrServiceActionUnavailable
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, runtimeStatus, desiredRuntimeStatus, instanceName string
	var nodeID *string
	err = tx.QueryRow(ctx, `SELECT status,runtime_status,coalesce(desired_runtime_status,''),instance_name,node_id FROM services WHERE id=$1 FOR UPDATE`, serviceID).
		Scan(&status, &runtimeStatus, &desiredRuntimeStatus, &instanceName, &nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrServiceNotFound
	}
	if err != nil {
		return "", err
	}
	token, _, err := security.NewToken()
	if err != nil {
		return "", err
	}
	if len(token) > 16 {
		token = token[:16]
	}
	dedup := fmt.Sprintf("%s:admin-%s:%s", serviceID, action, token)

	if action == "terminate" {
		if status == "terminating" || status == "terminated" {
			return "", ErrServiceActionUnavailable
		}
		if _, err := tx.Exec(ctx, `UPDATE provisioning_jobs SET status='dead',last_error='cancelled by service termination',locked_at=NULL,locked_by=NULL,updated_at=now() WHERE service_id=$1 AND status IN ('pending','failed')`, serviceID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status='void',updated_at=now() WHERE service_id=$1 AND status='open'`, serviceID); err != nil {
			return "", err
		}
		if nodeID == nil {
			// Nothing exists on a node, so close the record directly.
			if _, err := tx.Exec(ctx, `UPDATE services SET status='terminated',runtime_status='stopped',desired_runtime_status=NULL,terminated_at=now(),updated_at=now() WHERE id=$1`, serviceID); err != nil {
				return "", err
			}
			if _, err := tx.Exec(ctx, `UPDATE inventory_reservations SET status='released',released_at=now() WHERE service_id=$1 AND status='reserved'`, serviceID); err != nil {
				return "", err
			}
			if err := s.auditAdminAction(ctx, tx, staffID, serviceID, action, ip, userAgent, instanceName, ""); err != nil {
				return "", err
			}
			return "", tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `UPDATE services SET status='terminating',desired_runtime_status=NULL,updated_at=now() WHERE id=$1`, serviceID); err != nil {
			return "", err
		}
	} else {
		if status != "active" || nodeID == nil || desiredRuntimeStatus != "" {
			return "", ErrServiceActionUnavailable
		}
		if action == "start" && runtimeStatus == "running" || action != "start" && runtimeStatus == "stopped" {
			return "", ErrServiceActionUnavailable
		}
		desired := "running"
		if action == "stop" {
			desired = "stopped"
		}
		if _, err := tx.Exec(ctx, "UPDATE services SET desired_runtime_status=$2,updated_at=now() WHERE id=$1", serviceID, desired); err != nil {
			return "", err
		}
	}

	var jobID string
	err = tx.QueryRow(ctx, `
		INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload)
		VALUES($1,$2,$3,jsonb_build_object('requested_by',$4::text,'source','admin'))
		ON CONFLICT DO NOTHING RETURNING id
	`, serviceID, action, dedup, staffID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrActionInProgress
	}
	if err != nil {
		return "", err
	}
	if err := s.auditAdminAction(ctx, tx, staffID, serviceID, action, ip, userAgent, instanceName, jobID); err != nil {
		return "", err
	}
	return jobID, tx.Commit(ctx)
}

func (s *ProvisioningStore) auditAdminAction(ctx context.Context, tx pgx.Tx, staffID, serviceID, action, ip, userAgent, instanceName, jobID string) error {
	var ipValue any
	if strings.TrimSpace(ip) != "" {
		ipValue = strings.TrimSpace(ip)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,ip,user_agent,metadata)
		VALUES('staff',$1,$2,'service',$3,$4,$5,jsonb_build_object('instance_name',$6::text,'job_id',$7::text))
	`, staffID, "service.admin_"+action, serviceID, ipValue, userAgent, instanceName, jobID)
	return err
}
