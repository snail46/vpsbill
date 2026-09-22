package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MonitoringStore struct{ db *pgxpool.Pool }

func NewMonitoringStore(db *pgxpool.Pool) *MonitoringStore { return &MonitoringStore{db: db} }

type OperationalMetrics struct{ Accounts, OpenInvoices, ProvisioningJobs, FailedJobs, ActiveServices, UnhealthyNodes, OpenTickets, PendingOutbox int64 }

func (s *MonitoringStore) Snapshot(ctx context.Context) (OperationalMetrics, error) {
	var value OperationalMetrics
	err := s.db.QueryRow(ctx, `SELECT
	(SELECT count(*) FROM accounts WHERE status='active'),
	(SELECT count(*) FROM invoices WHERE status='open'),
	(SELECT count(*) FROM provisioning_jobs WHERE status IN ('pending','running')),
	(SELECT count(*) FROM provisioning_jobs WHERE status IN ('failed','dead')),
	(SELECT count(*) FROM services WHERE status='active'),
	(SELECT count(*) FROM nodes WHERE status<>'online'),
	(SELECT count(*) FROM support_tickets WHERE status NOT IN ('resolved','closed')),
	(SELECT count(*) FROM outbox_events WHERE published_at IS NULL)`).Scan(&value.Accounts, &value.OpenInvoices, &value.ProvisioningJobs, &value.FailedJobs, &value.ActiveServices, &value.UnhealthyNodes, &value.OpenTickets, &value.PendingOutbox)
	return value, err
}
