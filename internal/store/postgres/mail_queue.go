package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MailStore queues outgoing notification mail and answers the questions the
// reminder scanner asks.
type MailStore struct{ db *pgxpool.Pool }

func NewMailStore(db *pgxpool.Pool) *MailStore { return &MailStore{db: db} }

type QueuedMail struct {
	ID        string
	Recipient string
	Subject   string
	Body      string
	Attempts  int
}

// EnqueueMail adds a message unless one with the same dedup key was queued
// before; an empty key never deduplicates. It reports whether a row was added.
// EnqueueMail queues a message. Addresses that belong to a user who has not
// verified them are skipped, so a sign-up with someone else's address
// cannot make the platform mail them.
func (s *MailStore) EnqueueMail(ctx context.Context, recipient, subject, body, dedupKey string) (bool, error) {
	command, err := s.db.Exec(ctx, `
		INSERT INTO mail_queue(recipient,subject,body,dedup_key)
		SELECT $1,$2,$3,nullif($4,'')
		WHERE NOT EXISTS(SELECT 1 FROM users u WHERE lower(u.email)=lower($1) AND u.email_verified_at IS NULL)
		ON CONFLICT (dedup_key) DO NOTHING
	`, recipient, subject, body, dedupKey)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

// ClaimMail leases due messages; a lease that runs out (crashed sender) makes
// the message due again.
func (s *MailStore) ClaimMail(ctx context.Context, limit int) ([]QueuedMail, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE mail_queue SET status='sending', attempts=attempts+1, locked_until=now()+interval '2 minutes'
		WHERE id IN (
			SELECT id FROM mail_queue
			WHERE (status='pending' AND next_attempt_at<=now()) OR (status='sending' AND locked_until<now())
			ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		RETURNING id,recipient,subject,body,attempts
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]QueuedMail, 0)
	for rows.Next() {
		var row QueuedMail
		if err := rows.Scan(&row.ID, &row.Recipient, &row.Subject, &row.Body, &row.Attempts); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *MailStore) CompleteMail(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE mail_queue SET status='sent',sent_at=now(),locked_until=NULL,last_error=NULL WHERE id=$1`, id)
	return err
}

// FailMail schedules a retry, or gives up after maxAttempts.
func (s *MailStore) FailMail(ctx context.Context, id string, attempts, maxAttempts int, retryIn time.Duration, cause string) error {
	status := "pending"
	if attempts >= maxAttempts {
		status = "failed"
	}
	_, err := s.db.Exec(ctx, `
		UPDATE mail_queue SET status=$2,next_attempt_at=now()+make_interval(secs=>$3),locked_until=NULL,last_error=left($4,500)
		WHERE id=$1
	`, id, status, retryIn.Seconds(), cause)
	return err
}

// StaffEmails lists active staff login addresses, the fallback recipients
// for merchant notices.
func (s *MailStore) StaffEmails(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT u.email FROM staff_members sm JOIN users u ON u.id=sm.user_id WHERE u.status='active' ORDER BY u.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		result = append(result, email)
	}
	return result, rows.Err()
}

// ExpiringService is an active service whose renewal invoice is still open
// shortly before the due date.
type ExpiringService struct {
	ServiceID     string
	InstanceName  string
	PlanName      string
	DueAt         time.Time
	Email         string
	CustomerName  string
	InvoiceNumber string
	AmountMinor   int64
	Currency      string
}

func (s *MailStore) ExpiringServices(ctx context.Context, within time.Duration) ([]ExpiringService, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (s.id) s.id,s.instance_name,p.name,s.next_due_at,u.email,u.display_name,i.number,i.balance_minor,i.currency
		FROM services s
		JOIN plans p ON p.id=s.plan_id
		JOIN invoices i ON i.service_id=s.id AND i.kind='renewal' AND i.status='open'
		JOIN memberships m ON m.account_id=s.account_id
		JOIN users u ON u.id=m.user_id AND u.status='active'
		WHERE s.status='active' AND s.next_due_at>now() AND s.next_due_at<=now()+make_interval(secs=>$1)
		ORDER BY s.id, CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, m.created_at, i.due_at
	`, within.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ExpiringService, 0)
	for rows.Next() {
		var row ExpiringService
		if err := rows.Scan(&row.ServiceID, &row.InstanceName, &row.PlanName, &row.DueAt, &row.Email, &row.CustomerName, &row.InvoiceNumber, &row.AmountMinor, &row.Currency); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// TrafficTarget is a running service with a monthly traffic allowance.
type TrafficTarget struct {
	ServiceID    string
	InstanceName string
	PlanName     string
	TrafficGB    int
	Email        string
	CustomerName string
	// LockedMonth is the UTC+8 month (YYYY-MM) the instance was stopped for
	// using up its allowance, or empty.
	LockedMonth string
	NodeEndpoint
}

func (s *MailStore) TrafficTargets(ctx context.Context) ([]TrafficTarget, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (s.id) s.id,s.instance_name,p.name,p.traffic_gb,u.email,u.display_name,coalesce(s.traffic_locked_month,''),
		       n.provider_type,n.base_url,n.api_key_ciphertext,n.provider_options
		FROM services s
		JOIN plans p ON p.id=s.plan_id
		JOIN nodes n ON n.id=s.node_id
		JOIN memberships m ON m.account_id=s.account_id
		JOIN users u ON u.id=m.user_id AND u.status='active'
		WHERE s.status IN ('active','overdue')
		ORDER BY s.id, CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, m.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TrafficTarget, 0)
	for rows.Next() {
		var row TrafficTarget
		if err := rows.Scan(&row.ServiceID, &row.InstanceName, &row.PlanName, &row.TrafficGB, &row.Email, &row.CustomerName, &row.LockedMonth,
			&row.ProviderType, &row.BaseURL, &row.APIKeyCiphertext, &row.ProviderOptions); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// RecordServiceTraffic stores this month's two-way traffic; rx and tx are
// nil when the backend reports only a total.
func (s *MailStore) RecordServiceTraffic(ctx context.Context, serviceID string, totalBytes int64, rx, tx *int64) error {
	_, err := s.db.Exec(ctx, `UPDATE services SET traffic_used_bytes=$2,traffic_rx_bytes=$3,traffic_tx_bytes=$4,traffic_measured_at=now() WHERE id=$1`, serviceID, totalBytes, rx, tx)
	return err
}

// LockForTraffic stops an instance that used up its monthly allowance and
// blocks starting it until the month changes. It reports whether the lock
// is new.
func (s *MailStore) LockForTraffic(ctx context.Context, serviceID, month string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE services SET traffic_locked_month=$2,desired_runtime_status='stopped',updated_at=now() WHERE id=$1 AND status IN ('active','overdue') AND coalesce(traffic_locked_month,'')<>$2`, serviceID, month)
	if err != nil || command.RowsAffected() == 0 {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'stop',$2,jsonb_build_object('source','traffic_limit')) ON CONFLICT(deduplication_key) DO NOTHING`, serviceID, serviceID+":traffic-stop:"+month); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,action,target_type,target_id,metadata) VALUES('system','service.traffic_locked','service',$1,jsonb_build_object('month',$2::text))`, serviceID, month); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// UnlockTraffic lifts a lock from an earlier month and starts the instance
// again.
func (s *MailStore) UnlockTraffic(ctx context.Context, serviceID, month string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	err = tx.QueryRow(ctx, `UPDATE services SET traffic_locked_month=NULL,desired_runtime_status='running',updated_at=now() WHERE id=$1 AND traffic_locked_month IS NOT NULL AND traffic_locked_month<>$2 AND status='active' RETURNING $2::text`, serviceID, month).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'start',$2,jsonb_build_object('source','traffic_reset')) ON CONFLICT(deduplication_key) DO NOTHING`, serviceID, serviceID+":traffic-start:"+month); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NodeWatch is a node with a rental expiry or a transfer allowance, plus the
// traffic its services used this calendar month.
type NodeWatch struct {
	ID             string
	Name           string
	ExpiresAt      *time.Time
	TrafficQuotaGB int
	UsedBytes      int64
	// OwnerEmail is set for hosted nodes, whose host gets the reminders.
	OwnerEmail string
}

func (s *MailStore) NodeWatches(ctx context.Context) ([]NodeWatch, error) {
	rows, err := s.db.Query(ctx, `
		SELECT n.id,n.name,n.expires_at::timestamptz,n.traffic_quota_gb,
		       coalesce((SELECT sum(s.traffic_used_bytes) FROM services s
		                 WHERE s.node_id=n.id AND s.traffic_measured_at>=date_trunc('month',now())),0)::bigint,
		       coalesce(o.billing_email,'')
		FROM nodes n LEFT JOIN accounts o ON o.id=n.owner_account_id
		WHERE (n.expires_at IS NOT NULL OR n.traffic_quota_gb>0) AND n.retired_at IS NULL
		ORDER BY n.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NodeWatch, 0)
	for rows.Next() {
		var row NodeWatch
		if err := rows.Scan(&row.ID, &row.Name, &row.ExpiresAt, &row.TrafficQuotaGB, &row.UsedBytes, &row.OwnerEmail); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// TicketMailContext names the ticket and its requester for notification mail.
type TicketMailContext struct {
	Number         string
	Subject        string
	Priority       string
	CustomerName   string
	RequesterEmail string
	// HostName and HostEmail are set for tickets about a hosted instance.
	HostName  string
	HostEmail string
}

func (s *MailStore) TicketMailContext(ctx context.Context, ticketID string) (TicketMailContext, error) {
	var result TicketMailContext
	err := s.db.QueryRow(ctx, `
		SELECT t.number,t.subject,t.priority,a.display_name,u.email,coalesce(h.display_name,''),coalesce(h.billing_email,'')
		FROM support_tickets t JOIN accounts a ON a.id=t.account_id JOIN users u ON u.id=t.requester_user_id
		LEFT JOIN accounts h ON h.id=t.host_account_id
		WHERE t.id=$1
	`, ticketID).Scan(&result.Number, &result.Subject, &result.Priority, &result.CustomerName, &result.RequesterEmail, &result.HostName, &result.HostEmail)
	return result, err
}
