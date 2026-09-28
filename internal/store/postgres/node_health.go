package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"vpsbill/internal/provider"
)

// A node is held from new sales when its host stays overloaded. Short
// spikes are ignored: each threshold must hold for its whole window.
const (
	memLowPercent   = 5  // available memory below this share of the total
	diskFullPercent = 95 // instance storage used above this share
	loadPerCPU      = 3  // 15-minute load above this many times the CPU count
	memHoldAfter    = 30 * time.Minute
	diskHoldAfter   = 10 * time.Minute
	loadHoldAfter   = 24 * time.Hour
)

// healthSince records when each threshold was first crossed; nil means the
// node is currently within it.
type healthSince struct {
	Mem, Disk, Load *time.Time
}

// evaluateHealth advances the breach timestamps with a new sample and
// returns the reason to hold the node from sale, or "" when it may sell.
func evaluateHealth(health *provider.HostHealth, previous healthSince, now time.Time) (healthSince, string) {
	if health == nil {
		return healthSince{}, ""
	}
	track := func(breached bool, since *time.Time) *time.Time {
		switch {
		case !breached:
			return nil
		case since != nil:
			return since
		default:
			return &now
		}
	}
	var reasons []string
	if len(health.QuotaErrors) > 0 {
		names := make([]string, 0, len(health.QuotaErrors))
		for name := range health.QuotaErrors {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			reasons = append(reasons, fmt.Sprintf("%s 无法限制实例硬盘：%s", name, health.QuotaErrors[name]))
		}
	}
	next := healthSince{}
	next.Mem = track(health.MemTotalMB > 0 && health.MemAvailableMB*100 < health.MemTotalMB*memLowPercent, previous.Mem)
	if next.Mem != nil && now.Sub(*next.Mem) >= memHoldAfter {
		reasons = append(reasons, fmt.Sprintf("可用内存持续低于 %d%%（%d / %d MB）", memLowPercent, health.MemAvailableMB, health.MemTotalMB))
	}
	fullest, fullestPercent := "", int64(0)
	for _, disk := range health.Disks {
		if disk.TotalGB > 0 && disk.UsedGB*100/disk.TotalGB > fullestPercent {
			fullest, fullestPercent = disk.Name, disk.UsedGB*100/disk.TotalGB
		}
	}
	next.Disk = track(fullestPercent > diskFullPercent, previous.Disk)
	if next.Disk != nil && now.Sub(*next.Disk) >= diskHoldAfter {
		reasons = append(reasons, fmt.Sprintf("%s 实例存储已用 %d%%", fullest, fullestPercent))
	}
	next.Load = track(health.CPUs > 0 && health.Load15 > float64(health.CPUs*loadPerCPU), previous.Load)
	if next.Load != nil && now.Sub(*next.Load) >= loadHoldAfter {
		reasons = append(reasons, fmt.Sprintf("负载持续 24 小时超过核数的 %d 倍（%.1f / %d 核）", loadPerCPU, health.Load15, health.CPUs))
	}
	return next, strings.Join(reasons, "；")
}

// sellableCapacitySQL recomputes a node's sellable capacity: what its agent
// reports, lowered to any staff-verified cap, times the node's oversell
// ratio (never above the platform maximum).
const sellableCapacitySQL = `
	capacity_vcpu = floor(least(coalesce(reported_vcpu,0), coalesce(capacity_cap_vcpu, reported_vcpu, 0))
	                * least(overcommit_cpu, (SELECT max_overcommit_cpu FROM system_settings WHERE singleton=true)))::int,
	capacity_ram_mb = floor(least(coalesce(reported_ram_mb,0), coalesce(capacity_cap_ram_mb, reported_ram_mb, 0))
	                * least(overcommit_ram, (SELECT max_overcommit_ram FROM system_settings WHERE singleton=true)))::bigint,
	capacity_disk_gb = floor(least(coalesce(reported_disk_gb,0), coalesce(capacity_cap_disk_gb, reported_disk_gb, 0))
	                * least(overcommit_disk, (SELECT max_overcommit_disk FROM system_settings WHERE singleton=true)))::bigint`

// RecordNodeReport stores an online node's report: its raw info, the
// capacity its agent offers (scaled into sellable capacity), the machine it
// runs on and its health, holding it from sale while overloaded.
func (s *CatalogStore) RecordNodeReport(ctx context.Context, id string, info provider.HostInfo) error {
	raw, _ := json.Marshal(info.Raw)
	var health []byte
	if info.Health != nil {
		health, _ = json.Marshal(info.Health)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previous healthSince
	if err := tx.QueryRow(ctx, `SELECT health_mem_since, health_disk_since, health_load_since FROM nodes WHERE id=$1 FOR UPDATE`, id).
		Scan(&previous.Mem, &previous.Disk, &previous.Load); err != nil {
		return err
	}
	since, reason := evaluateHealth(info.Health, previous, time.Now())
	if _, err := tx.Exec(ctx, `
		UPDATE nodes SET status='online', capacity=$2, last_seen_at=now(), updated_at=now(),
		    reported_vcpu=$3, reported_ram_mb=$4, reported_disk_gb=$5, machine_id=nullif($6,''),
		    health=$7::jsonb, health_mem_since=$8, health_disk_since=$9, health_load_since=$10,
		    health_hold_since=CASE WHEN nullif($11,'') IS NULL THEN NULL ELSE coalesce(health_hold_since, now()) END,
		    health_hold_reason=nullif($11,'')
		WHERE id=$1
	`, id, raw, info.Capacity.VCPU, info.Capacity.RAMMB, info.Capacity.DiskGB, info.MachineID, health,
		since.Mem, since.Disk, since.Load, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET `+sellableCapacitySQL+` WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Overcommit is a node's oversell ratio per resource; 1 sells exactly what
// the agent reports.
type Overcommit struct {
	CPU     float64 `json:"cpu"`
	RAM     float64 `json:"ram"`
	Disk    float64 `json:"disk"`
	Traffic float64 `json:"traffic"`
}

// OvercommitLimits are the platform maximums, from site settings.
type OvercommitLimits = Overcommit

var ErrOvercommitInvalid = errors.New("invalid oversell ratio")

// Validate checks each ratio lies between 1 and the platform maximum.
func (o Overcommit) Validate(limits OvercommitLimits) error {
	checks := []struct {
		name         string
		value, limit float64
	}{{"CPU", o.CPU, limits.CPU}, {"内存", o.RAM, limits.RAM}, {"硬盘", o.Disk, limits.Disk}, {"流量", o.Traffic, limits.Traffic}}
	for _, check := range checks {
		if math.IsNaN(check.value) || check.value < 1 || check.value > check.limit {
			return fmt.Errorf("%w: %s 超售倍数必须在 1–%g 之间", ErrOvercommitInvalid, check.name, check.limit)
		}
	}
	return nil
}

// SetOvercommit changes a node's oversell ratios and its sellable capacity
// right away. ownerID limits the change to the host's own node; empty is
// staff.
func (s *CatalogStore) SetOvercommit(ctx context.Context, nodeID, ownerID string, ratios Overcommit, limits OvercommitLimits) error {
	if err := ratios.Validate(limits); err != nil {
		return err
	}
	command, err := s.db.Exec(ctx, `
		UPDATE nodes SET overcommit_cpu=round($3::numeric,2), overcommit_ram=round($4::numeric,2),
		       overcommit_disk=round($5::numeric,2), overcommit_traffic=round($6::numeric,2), updated_at=now()
		WHERE id=$1 AND ($2='' OR owner_account_id::text=$2) AND retired_at IS NULL
	`, nodeID, ownerID, ratios.CPU, ratios.RAM, ratios.Disk, ratios.Traffic)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrHostedNodeNotFound
	}
	_, err = s.db.Exec(ctx, `UPDATE nodes SET `+sellableCapacitySQL+` WHERE id=$1`, nodeID)
	return err
}

// NodeSupply shows how a node's sellable capacity is derived and whether it
// may sell right now.
type NodeSupply struct {
	ReportedVCPU   int                  `json:"reported_vcpu"`
	ReportedRAMMB  int64                `json:"reported_ram_mb"`
	ReportedDiskGB int64                `json:"reported_disk_gb"`
	Overcommit     Overcommit           `json:"overcommit"`
	Health         *provider.HostHealth `json:"health,omitempty"`
	HoldReason     string               `json:"health_hold_reason,omitempty"`
	HoldSince      *time.Time           `json:"health_hold_since,omitempty"`
	// SharedMachine is set when other nodes' agents run on the same
	// machine; SharedWith names them (staff views only).
	SharedMachine bool     `json:"shared_machine"`
	SharedWith    []string `json:"shared_machine_with,omitempty"`
	// SoldTrafficGB is the monthly traffic of the node's live instances.
	SoldTrafficGB int64 `json:"sold_traffic_gb"`
	healthJSON    []byte
}

const nodeSupplyColumns = `coalesce(n.reported_vcpu,0), coalesce(n.reported_ram_mb,0), coalesce(n.reported_disk_gb,0),
	n.overcommit_cpu::float8, n.overcommit_ram::float8, n.overcommit_disk::float8, n.overcommit_traffic::float8,
	n.health, coalesce(n.health_hold_reason,''), n.health_hold_since,
	coalesce((SELECT array_agg(m.name ORDER BY m.name) FROM nodes m WHERE m.machine_id=n.machine_id AND m.id<>n.id AND m.retired_at IS NULL), '{}'),
	coalesce((SELECT sum(tp.traffic_gb) FROM services ts JOIN plans tp ON tp.id=ts.plan_id
	          WHERE ts.node_id=n.id AND ts.status NOT IN ('terminating','terminated')),0)::bigint`

func (s *NodeSupply) targets() []any {
	return []any{&s.ReportedVCPU, &s.ReportedRAMMB, &s.ReportedDiskGB,
		&s.Overcommit.CPU, &s.Overcommit.RAM, &s.Overcommit.Disk, &s.Overcommit.Traffic,
		&s.healthJSON, &s.HoldReason, &s.HoldSince, &s.SharedWith, &s.SoldTrafficGB}
}

// finish decodes scanned fields; names of nodes sharing the machine are
// kept only for staff.
func (s *NodeSupply) finish(staff bool) {
	if len(s.healthJSON) > 0 {
		s.Health = &provider.HostHealth{}
		_ = json.Unmarshal(s.healthJSON, s.Health)
	}
	s.SharedMachine = len(s.SharedWith) > 0
	if !staff {
		s.SharedWith = nil
	}
}

// HeldNode is a node held from sale for overload, for notification.
type HeldNode struct {
	ID, Name, Reason     string
	Since                time.Time
	OwnerName, OwnerMail string
}

// HeldNodes lists the nodes currently held from sale.
func (s *MailStore) HeldNodes(ctx context.Context) ([]HeldNode, error) {
	rows, err := s.db.Query(ctx, `
		SELECT n.id, n.name, n.health_hold_reason, n.health_hold_since, coalesce(a.display_name,''), coalesce(a.billing_email,'')
		FROM nodes n LEFT JOIN accounts a ON a.id=n.owner_account_id
		WHERE n.health_hold_reason IS NOT NULL AND n.retired_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HeldNode
	for rows.Next() {
		var node HeldNode
		if err := rows.Scan(&node.ID, &node.Name, &node.Reason, &node.Since, &node.OwnerName, &node.OwnerMail); err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, rows.Err()
}
