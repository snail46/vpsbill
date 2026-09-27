package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MonitoringStore struct{ db *pgxpool.Pool }

func NewMonitoringStore(db *pgxpool.Pool) *MonitoringStore { return &MonitoringStore{db: db} }

type OperationalMetrics struct{ Accounts, OpenInvoices, ProvisioningJobs, FailedJobs, ActiveServices, UnhealthyNodes, OpenTickets, PendingOutbox int64 }

type MoneyTotal struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}
type OperationsOverview struct {
	Accounts        int64        `json:"accounts"`
	Orders30Days    int64        `json:"orders_30_days"`
	Services        int64        `json:"services"`
	RunningServices int64        `json:"running_services"`
	OpenInvoices    int64        `json:"open_invoices"`
	OverdueServices int64        `json:"overdue_services"`
	Nodes           int64        `json:"nodes"`
	OnlineNodes     int64        `json:"online_nodes"`
	OpenTickets     int64        `json:"open_tickets"`
	PendingJobs     int64        `json:"pending_jobs"`
	FailedJobs      int64        `json:"failed_jobs"`
	CapacityVCPU    int64        `json:"capacity_vcpu"`
	ReservedVCPU    int64        `json:"reserved_vcpu"`
	CapacityRAMMB   int64        `json:"capacity_ram_mb"`
	ReservedRAMMB   int64        `json:"reserved_ram_mb"`
	CapacityDiskGB  int64        `json:"capacity_disk_gb"`
	ReservedDiskGB  int64        `json:"reserved_disk_gb"`
	Revenue30Days   []MoneyTotal `json:"revenue_30_days"`
	Outstanding     []MoneyTotal `json:"outstanding"`
}

type HostProbe struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	ProviderType        string         `json:"provider_type"`
	RegionCode          string         `json:"region_code"`
	RegionName          string         `json:"region_name"`
	BaseURL             string         `json:"base_url"`
	Status              string         `json:"status"`
	VirtualizationTypes []string       `json:"virtualization_types"`
	Capacity            map[string]any `json:"capacity"`
	CapacityVCPU        int64          `json:"capacity_vcpu"`
	ReservedVCPU        int64          `json:"reserved_vcpu"`
	CapacityRAMMB       int64          `json:"capacity_ram_mb"`
	ReservedRAMMB       int64          `json:"reserved_ram_mb"`
	CapacityDiskGB      int64          `json:"capacity_disk_gb"`
	ReservedDiskGB      int64          `json:"reserved_disk_gb"`
	LastSeenAt          *time.Time     `json:"last_seen_at"`
}

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

func (s *MonitoringStore) Overview(ctx context.Context) (OperationsOverview, error) {
	var value OperationsOverview
	err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM accounts WHERE status='active'),(SELECT count(*) FROM orders WHERE created_at>=now()-interval '30 days'),
		(SELECT count(*) FROM services WHERE status<>'terminated'),(SELECT count(*) FROM services WHERE runtime_status='running'),
		(SELECT count(*) FROM invoices WHERE status='open'),(SELECT count(*) FROM services WHERE status IN ('overdue','suspended')),
		(SELECT count(*) FROM nodes),(SELECT count(*) FROM nodes WHERE status='online'),
		(SELECT count(*) FROM support_tickets WHERE status NOT IN ('resolved','closed')),
		(SELECT count(*) FROM provisioning_jobs WHERE status IN ('pending','running')),(SELECT count(*) FROM provisioning_jobs WHERE status IN ('failed','dead')),
		coalesce((SELECT sum(capacity_vcpu) FROM nodes),0),coalesce((SELECT sum(vcpu) FROM inventory_reservations WHERE status='reserved'),0),
		coalesce((SELECT sum(capacity_ram_mb) FROM nodes),0),coalesce((SELECT sum(ram_mb) FROM inventory_reservations WHERE status='reserved'),0),
		coalesce((SELECT sum(capacity_disk_gb) FROM nodes),0),coalesce((SELECT sum(disk_gb) FROM inventory_reservations WHERE status='reserved'),0)`).Scan(
		&value.Accounts, &value.Orders30Days, &value.Services, &value.RunningServices, &value.OpenInvoices, &value.OverdueServices, &value.Nodes, &value.OnlineNodes, &value.OpenTickets, &value.PendingJobs, &value.FailedJobs,
		&value.CapacityVCPU, &value.ReservedVCPU, &value.CapacityRAMMB, &value.ReservedRAMMB, &value.CapacityDiskGB, &value.ReservedDiskGB)
	if err != nil {
		return value, err
	}
	value.Revenue30Days, err = s.moneyTotals(ctx, `SELECT currency,sum(amount_minor) FROM transactions WHERE type='payment' AND status='succeeded' AND created_at>=now()-interval '30 days' GROUP BY currency ORDER BY currency`)
	if err != nil {
		return value, err
	}
	value.Outstanding, err = s.moneyTotals(ctx, `SELECT currency,sum(balance_minor) FROM invoices WHERE status='open' GROUP BY currency ORDER BY currency`)
	return value, err
}

func (s *MonitoringStore) moneyTotals(ctx context.Context, query string) ([]MoneyTotal, error) {
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MoneyTotal, 0)
	for rows.Next() {
		var item MoneyTotal
		if err := rows.Scan(&item.Currency, &item.AmountMinor); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *MonitoringStore) Hosts(ctx context.Context) ([]HostProbe, error) {
	rows, err := s.db.Query(ctx, `SELECT n.id,n.name,n.provider_type,r.code,r.name,n.base_url,n.status,n.virtualization_types,n.capacity,n.capacity_vcpu,
		coalesce(sum(i.vcpu) filter(where i.status='reserved'),0),n.capacity_ram_mb,coalesce(sum(i.ram_mb) filter(where i.status='reserved'),0),n.capacity_disk_gb,coalesce(sum(i.disk_gb) filter(where i.status='reserved'),0),n.last_seen_at
		FROM nodes n JOIN regions r ON r.id=n.region_id LEFT JOIN inventory_reservations i ON i.node_id=n.id GROUP BY n.id,r.code,r.name ORDER BY r.code,n.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]HostProbe, 0)
	for rows.Next() {
		var item HostProbe
		var raw []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.ProviderType, &item.RegionCode, &item.RegionName, &item.BaseURL, &item.Status, &item.VirtualizationTypes, &raw, &item.CapacityVCPU, &item.ReservedVCPU, &item.CapacityRAMMB, &item.ReservedRAMMB, &item.CapacityDiskGB, &item.ReservedDiskGB, &item.LastSeenAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Capacity)
		result = append(result, item)
	}
	return result, rows.Err()
}
