package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoCapacity = errors.New("no node has enough available capacity")

type ProvisioningStore struct {
	db *pgxpool.Pool
}

func NewProvisioningStore(db *pgxpool.Pool) *ProvisioningStore {
	return &ProvisioningStore{db: db}
}

type ProvisioningJob struct {
	ID           string         `json:"id"`
	ServiceID    string         `json:"service_id"`
	InstanceName string         `json:"instance_name,omitempty"`
	CustomerName string         `json:"customer_name,omitempty"`
	Action       string         `json:"action"`
	Status       string         `json:"status"`
	Attempts     int            `json:"attempts"`
	AvailableAt  time.Time      `json:"available_at"`
	LockedAt     *time.Time     `json:"locked_at,omitempty"`
	LockedBy     string         `json:"-"`
	LastError    string         `json:"last_error,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type ProvisionContext struct {
	JobID        string
	ServiceID    string
	InstanceName string
	NodeID       string
	NodeName     string
	NodeEndpoint
	Virtualization  string
	VCPU            int
	RAMMB           int
	DiskGB          int
	TrafficGB       int
	NetworkDownMbps int
	NetworkUpMbps   int
	SnapshotLimit   int
	ExpiresAt       *time.Time
	Configuration   map[string]any
}

type ServiceRecord struct {
	ID                 string     `json:"id"`
	CustomerName       string     `json:"customer_name"`
	PlanName           string     `json:"plan_name"`
	RegionName         string     `json:"region_name"`
	NodeName           string     `json:"node_name,omitempty"`
	Status             string     `json:"status"`
	RuntimeStatus      string     `json:"runtime_status"`
	InstanceName       string     `json:"instance_name"`
	ExternalID         string     `json:"external_id,omitempty"`
	PrimaryIPv4        string     `json:"primary_ipv4,omitempty"`
	PrimaryIPv6        string     `json:"primary_ipv6,omitempty"`
	NextDueAt          *time.Time `json:"next_due_at,omitempty"`
	LastReconciledAt   *time.Time `json:"last_reconciled_at,omitempty"`
	LastReconcileError string     `json:"last_reconcile_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type ReconcileTarget struct {
	ServiceID    string
	InstanceName string
	NodeEndpoint
}

type ActionContext struct {
	JobID        string
	ServiceID    string
	InstanceName string
	NodeEndpoint
}

// ClaimJob leases one due job. SKIP LOCKED allows more worker replicas without
// executing the same row concurrently.
func (s *ProvisioningStore) ClaimJob(ctx context.Context, workerID string) (ProvisioningJob, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ProvisioningJob{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job ProvisioningJob
	var payload []byte
	err = tx.QueryRow(ctx, `
		SELECT id, service_id, action, status, attempts, available_at, coalesce(last_error,''), payload, created_at, updated_at
		FROM provisioning_jobs
		WHERE status IN ('pending','failed') AND available_at <= now()
		ORDER BY available_at, created_at
		FOR UPDATE SKIP LOCKED LIMIT 1
	`).Scan(&job.ID, &job.ServiceID, &job.Action, &job.Status, &job.Attempts, &job.AvailableAt, &job.LastError, &payload, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProvisioningJob{}, false, nil
	}
	if err != nil {
		return ProvisioningJob{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE provisioning_jobs SET status='running', attempts=attempts+1, locked_at=now(), locked_by=$2,
		    last_error=NULL, updated_at=now() WHERE id=$1
	`, job.ID, workerID); err != nil {
		return ProvisioningJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProvisioningJob{}, false, err
	}
	job.Status = "running"
	job.Attempts++
	job.LockedBy = workerID
	_ = json.Unmarshal(payload, &job.Payload)
	return job, true, nil
}

func (s *ProvisioningStore) RecoverStaleJobs(ctx context.Context, staleAfter time.Duration) (int64, error) {
	command, err := s.db.Exec(ctx, `
		UPDATE provisioning_jobs SET status='failed', available_at=now(), locked_at=NULL, locked_by=NULL,
		    last_error='worker lease expired; task recovered', updated_at=now()
		WHERE status='running' AND locked_at < now() - make_interval(secs => $1)
	`, int(staleAfter.Seconds()))
	return command.RowsAffected(), err
}

// ReserveNode serializes assignment through a service row and candidate node
// lock. Reservations, rather than observed VM state, are the source of truth
// for available sellable capacity.
func (s *ProvisioningStore) ReserveNode(ctx context.Context, serviceID string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentNode *string
	var regionID, virtualization, providerType string
	var vcpu, ramMB, diskGB int
	if err := tx.QueryRow(ctx, `
		SELECT s.node_id, s.region_id, p.virtualization, p.provider_type, p.vcpu, p.ram_mb, p.disk_gb
		FROM services s JOIN plans p ON p.id=s.plan_id
		WHERE s.id=$1 FOR UPDATE OF s
	`, serviceID).Scan(&currentNode, &regionID, &virtualization, &providerType, &vcpu, &ramMB, &diskGB); err != nil {
		return "", err
	}
	if currentNode != nil {
		return *currentNode, tx.Commit(ctx)
	}

	var nodeID string
	err = tx.QueryRow(ctx, `
		SELECT n.id
		FROM nodes n
		WHERE n.region_id=$1 AND n.status='online' AND $2=ANY(n.virtualization_types) AND n.provider_type=$6
		  AND n.capacity_vcpu - coalesce((SELECT sum(r.vcpu) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0) >= $3
		  AND n.capacity_ram_mb - coalesce((SELECT sum(r.ram_mb) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0) >= $4
		  AND n.capacity_disk_gb - coalesce((SELECT sum(r.disk_gb) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0) >= $5
		ORDER BY n.capacity_ram_mb - coalesce((SELECT sum(r.ram_mb) FROM inventory_reservations r WHERE r.node_id=n.id AND r.status='reserved'),0)
		FOR UPDATE OF n SKIP LOCKED LIMIT 1
	`, regionID, virtualization, vcpu, ramMB, diskGB, providerType).Scan(&nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoCapacity
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_reservations(service_id,node_id,vcpu,ram_mb,disk_gb,status,released_at)
		VALUES($1,$2,$3,$4,$5,'reserved',NULL)
		ON CONFLICT(service_id) DO UPDATE SET node_id=excluded.node_id, vcpu=excluded.vcpu,
		    ram_mb=excluded.ram_mb, disk_gb=excluded.disk_gb, status='reserved', released_at=NULL
	`, serviceID, nodeID, vcpu, ramMB, diskGB); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "UPDATE services SET node_id=$2, runtime_status='creating', updated_at=now() WHERE id=$1", serviceID, nodeID); err != nil {
		return "", err
	}
	return nodeID, tx.Commit(ctx)
}

func (s *ProvisioningStore) ProvisionContext(ctx context.Context, jobID string) (ProvisionContext, error) {
	var result ProvisionContext
	var payload []byte
	err := s.db.QueryRow(ctx, `
		SELECT j.id, s.id, s.instance_name, n.id, n.name, n.provider_type, n.base_url, n.api_key_ciphertext,n.provider_options,
		       p.virtualization, p.vcpu, p.ram_mb, p.disk_gb, p.traffic_gb,
		       p.network_down_mbps, p.network_up_mbps, p.snapshot_limit, s.expires_at, j.payload
		FROM provisioning_jobs j
		JOIN services s ON s.id=j.service_id
		JOIN plans p ON p.id=s.plan_id
		JOIN nodes n ON n.id=s.node_id
		WHERE j.id=$1
	`, jobID).Scan(&result.JobID, &result.ServiceID, &result.InstanceName, &result.NodeID, &result.NodeName,
		&result.ProviderType, &result.BaseURL, &result.APIKeyCiphertext, &result.ProviderOptions, &result.Virtualization, &result.VCPU, &result.RAMMB,
		&result.DiskGB, &result.TrafficGB, &result.NetworkDownMbps, &result.NetworkUpMbps,
		&result.SnapshotLimit, &result.ExpiresAt, &payload)
	if err != nil {
		return ProvisionContext{}, err
	}
	var root map[string]any
	_ = json.Unmarshal(payload, &root)
	result.Configuration, _ = root["configuration"].(map[string]any)
	if result.Configuration == nil {
		result.Configuration = map[string]any{}
	}
	return result, nil
}

func (s *ProvisioningStore) CompleteProvision(ctx context.Context, jobID, workerID, externalID, externalUUID, ipv4, ipv6, runtimeStatus string, rootPasswordCiphertext []byte) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var serviceID string
	err = tx.QueryRow(ctx, `
		UPDATE provisioning_jobs SET status='succeeded', locked_at=NULL, locked_by=NULL, last_error=NULL, updated_at=now()
		WHERE id=$1 AND status='running' AND locked_by=$2 RETURNING service_id
	`, jobID, workerID).Scan(&serviceID)
	if err != nil {
		return fmt.Errorf("complete leased job: %w", err)
	}
	if runtimeStatus == "" {
		runtimeStatus = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE services SET status='active', runtime_status=$2, external_id=nullif($3,''), external_uuid=nullif($4,''),
		    primary_ipv4=nullif($5,'')::inet, primary_ipv6=nullif($6,'')::inet,
		    root_password_ciphertext=coalesce($7,root_password_ciphertext),
		    last_reconciled_at=now(), last_reconcile_error=NULL, version=version+1, updated_at=now()
		WHERE id=$1
	`, serviceID, runtimeStatus, externalID, externalUUID, ipv4, ipv6, rootPasswordCiphertext); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orders o SET status='completed', updated_at=now()
		WHERE o.id=(SELECT oi.order_id FROM services s JOIN order_items oi ON oi.id=s.order_item_id WHERE s.id=$1)
		AND NOT EXISTS (
			SELECT 1 FROM order_items oi LEFT JOIN services s ON s.order_item_id=oi.id
			WHERE oi.order_id=o.id AND (s.id IS NULL OR s.status <> 'active')
		)
	`, serviceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload)
		VALUES('service',$1,'service.activated',$2,jsonb_build_object('job_id',$3::text))
		ON CONFLICT(deduplication_key) DO NOTHING
	`, serviceID, serviceID+":activated:v1", jobID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ProvisioningStore) ActionContext(ctx context.Context, jobID string) (ActionContext, error) {
	var result ActionContext
	err := s.db.QueryRow(ctx, `
		SELECT j.id,s.id,s.instance_name,n.provider_type,n.base_url,n.api_key_ciphertext,n.provider_options
		FROM provisioning_jobs j JOIN services s ON s.id=j.service_id JOIN nodes n ON n.id=s.node_id
		WHERE j.id=$1
	`, jobID).Scan(&result.JobID, &result.ServiceID, &result.InstanceName, &result.ProviderType, &result.BaseURL, &result.APIKeyCiphertext, &result.ProviderOptions)
	return result, err
}

func (s *ProvisioningStore) CompleteAction(ctx context.Context, jobID, workerID, externalTaskID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var serviceID, action string
	err = tx.QueryRow(ctx, `
		UPDATE provisioning_jobs SET status='succeeded',external_task_id=nullif($3,''),locked_at=NULL,locked_by=NULL,last_error=NULL,updated_at=now()
		WHERE id=$1 AND status='running' AND locked_by=$2 RETURNING service_id,action
	`, jobID, workerID, externalTaskID).Scan(&serviceID, &action)
	if err != nil {
		return fmt.Errorf("complete leased action: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE services SET desired_runtime_status=CASE WHEN $2='restart' THEN NULL ELSE desired_runtime_status END,last_reconcile_error=NULL,updated_at=now() WHERE id=$1", serviceID, action); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ProvisioningStore) CompleteTermination(ctx context.Context, jobID, workerID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var serviceID string
	err = tx.QueryRow(ctx, `UPDATE provisioning_jobs SET status='succeeded',locked_at=NULL,locked_by=NULL,last_error=NULL,updated_at=now() WHERE id=$1 AND status='running' AND locked_by=$2 AND action='terminate' RETURNING service_id`, jobID, workerID).Scan(&serviceID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE services SET status='terminated',runtime_status='stopped',desired_runtime_status=NULL,terminated_at=now(),node_id=NULL,primary_ipv4=NULL,primary_ipv6=NULL,updated_at=now() WHERE id=$1`, serviceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE inventory_reservations SET status='released',released_at=now() WHERE service_id=$1 AND status='reserved'`, serviceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.terminated',$2,'{}') ON CONFLICT(deduplication_key) DO NOTHING`, serviceID, serviceID+":terminated:v1"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ProvisioningStore) FailJob(ctx context.Context, job ProvisioningJob, cause error, maxAttempts int) error {
	message := strings.TrimSpace(cause.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	dead := job.Attempts >= maxAttempts
	delay := time.Duration(1<<min(job.Attempts, 9)) * time.Second
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	status := "failed"
	if dead {
		status = "dead"
	}
	command, err := tx.Exec(ctx, `
		UPDATE provisioning_jobs SET status=$3, available_at=now()+make_interval(secs => $4), locked_at=NULL, locked_by=NULL,
		    last_error=$5, updated_at=now() WHERE id=$1 AND status='running' AND locked_by=$2
	`, job.ID, job.LockedBy, status, int(delay.Seconds()), message)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("job lease no longer belongs to this worker")
	}
	if dead && job.Action == "provision" {
		if _, err := tx.Exec(ctx, "UPDATE services SET status='error', runtime_status='error', node_id=NULL, updated_at=now() WHERE id=$1", job.ServiceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE inventory_reservations SET status='released', released_at=now() WHERE service_id=$1 AND status='reserved'", job.ServiceID); err != nil {
			return err
		}
	} else if dead && job.Action == "terminate" {
		if _, err := tx.Exec(ctx, "UPDATE services SET status='error',last_reconcile_error=$2,updated_at=now() WHERE id=$1", job.ServiceID, "termination failed: "+message); err != nil {
			return err
		}
	} else if dead {
		if _, err := tx.Exec(ctx, "UPDATE services SET desired_runtime_status=NULL,last_reconcile_error=$2,updated_at=now() WHERE id=$1", job.ServiceID, "automation action failed: "+message); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *ProvisioningStore) RetryJob(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var serviceID, action string
	err = tx.QueryRow(ctx, `
		UPDATE provisioning_jobs SET status='pending', attempts=0, available_at=now(), locked_at=NULL,
		    locked_by=NULL, last_error=NULL, updated_at=now()
		WHERE id=$1 AND status IN ('failed','dead') RETURNING service_id,action
	`, id).Scan(&serviceID, &action)
	if err != nil {
		return err
	}
	if action == "provision" {
		if _, err := tx.Exec(ctx, `
			UPDATE services SET status='provisioning', runtime_status='unknown',
			    node_id=CASE WHEN EXISTS(SELECT 1 FROM inventory_reservations r WHERE r.service_id=$1 AND r.status='reserved') THEN node_id ELSE NULL END,
			    updated_at=now() WHERE id=$1
		`, serviceID); err != nil {
			return err
		}
	} else {
		desired := "running"
		if action == "stop" {
			desired = "stopped"
		}
		if _, err := tx.Exec(ctx, "UPDATE services SET desired_runtime_status=$2,last_reconcile_error=NULL,updated_at=now() WHERE id=$1", serviceID, desired); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *ProvisioningStore) ListServices(ctx context.Context) ([]ServiceRecord, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id,a.display_name,p.name,r.name,coalesce(n.name,''),s.status,s.runtime_status,s.instance_name,
		       coalesce(s.external_id,''),coalesce(host(s.primary_ipv4),''),coalesce(host(s.primary_ipv6),''),
		       s.next_due_at,s.last_reconciled_at,coalesce(s.last_reconcile_error,''),s.created_at
		FROM services s JOIN accounts a ON a.id=s.account_id JOIN plans p ON p.id=s.plan_id
		JOIN regions r ON r.id=s.region_id LEFT JOIN nodes n ON n.id=s.node_id
		ORDER BY s.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ServiceRecord, 0)
	for rows.Next() {
		var row ServiceRecord
		if err := rows.Scan(&row.ID, &row.CustomerName, &row.PlanName, &row.RegionName, &row.NodeName, &row.Status, &row.RuntimeStatus,
			&row.InstanceName, &row.ExternalID, &row.PrimaryIPv4, &row.PrimaryIPv6, &row.NextDueAt, &row.LastReconciledAt, &row.LastReconcileError, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *ProvisioningStore) ListJobs(ctx context.Context) ([]ProvisioningJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT j.id,j.service_id,s.instance_name,a.display_name,j.action,j.status,j.attempts,j.available_at,
		       j.locked_at,coalesce(j.last_error,''),j.created_at,j.updated_at
		FROM provisioning_jobs j JOIN services s ON s.id=j.service_id JOIN accounts a ON a.id=s.account_id
		ORDER BY j.created_at DESC LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProvisioningJob, 0)
	for rows.Next() {
		var row ProvisioningJob
		if err := rows.Scan(&row.ID, &row.ServiceID, &row.InstanceName, &row.CustomerName, &row.Action, &row.Status, &row.Attempts,
			&row.AvailableAt, &row.LockedAt, &row.LastError, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *ProvisioningStore) ListReconcileTargets(ctx context.Context) ([]ReconcileTarget, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id,s.instance_name,n.provider_type,n.base_url,n.api_key_ciphertext,n.provider_options
		FROM services s JOIN nodes n ON n.id=s.node_id
		WHERE s.status IN ('active','suspended','overdue')
		ORDER BY s.last_reconciled_at NULLS FIRST
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ReconcileTarget, 0)
	for rows.Next() {
		var row ReconcileTarget
		if err := rows.Scan(&row.ServiceID, &row.InstanceName, &row.ProviderType, &row.BaseURL, &row.APIKeyCiphertext, &row.ProviderOptions); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *ProvisioningStore) UpdateReconciledService(ctx context.Context, serviceID, runtimeStatus, externalID, externalUUID, ipv4, ipv6, reconcileError string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE services SET runtime_status=$2,
		    desired_runtime_status=CASE WHEN desired_runtime_status=$2 THEN NULL ELSE desired_runtime_status END,
		    external_id=coalesce(nullif($3,''),external_id),
		    external_uuid=coalesce(nullif($4,''),external_uuid), primary_ipv4=coalesce(nullif($5,'')::inet,primary_ipv4),
		    primary_ipv6=coalesce(nullif($6,'')::inet,primary_ipv6), last_reconciled_at=now(),
		    last_reconcile_error=nullif($7,''), updated_at=now() WHERE id=$1
	`, serviceID, runtimeStatus, externalID, externalUUID, ipv4, ipv6, reconcileError)
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
