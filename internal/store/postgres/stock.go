package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"vpsbill/internal/provider"
)

// planHeldSQL counts a plan's live instances plus units in orders that can
// still be paid; it takes the plan id as p.id.
const planHeldSQL = `((SELECT count(*) FROM services hs WHERE hs.plan_id=p.id AND hs.status NOT IN ('terminating','terminated'))::int
	+ coalesce((SELECT sum(hoi.quantity) FROM order_items hoi JOIN invoices hi ON hi.order_id=hoi.order_id
	            WHERE hoi.plan_id=p.id AND hi.status='open' AND hi.due_at>now()),0)::int)`

// priceSoldSQL counts units ordered at a plan and cycle, paid or still
// payable; it takes the plan id as $1 and the cycle as $2.
const priceSoldSQL = `coalesce((SELECT sum(soi.quantity) FROM order_items soi JOIN invoices si ON si.order_id=soi.order_id
	WHERE soi.plan_id=$1 AND soi.configuration->>'billing_cycle'=$2 AND (si.status='paid' OR (si.status='open' AND si.due_at>now()))),0)::int`

// StockCapacity is how many instances a plan may be given as stock: the
// sellable capacity of the nodes it sells on (hardware x oversell ratio),
// less what the other plans on those nodes hold as stock or have sold, less
// instances of plans outside that set, divided by the plan's size.
type StockCapacity struct {
	Max  int `json:"max"`
	Held int `json:"held"`
	// Free* is what is left for this plan before dividing by its size.
	FreeVCPU      int64  `json:"free_vcpu"`
	FreeRAMMB     int64  `json:"free_ram_mb"`
	FreeDiskGB    int64  `json:"free_disk_gb"`
	FreeTrafficGB *int64 `json:"free_traffic_gb,omitempty"`
	Nodes         int    `json:"nodes"`
	// DiskIO suggests per-instance disk limits; nil until a node of the
	// pool has reported its disk speed.
	DiskIO *DiskIOSuggestion `json:"disk_io,omitempty"`
}

// DiskIOSuggestion is a disk limit for each instance of a plan, from the
// weakest machine's measured disk and how many of the plan's instances it
// holds when full (Instances).
type DiskIOSuggestion struct {
	provider.DiskIO
	Host      provider.DiskPerf `json:"host"`
	Instances int               `json:"instances"`
	// Unsupported lists nodes whose storage ignores disk limits.
	Unsupported []string `json:"unsupported,omitempty"`
}

// suggestDiskIO shares a disk among the instances likely to be busy at
// once: a quarter of them, but at least two, so one instance never takes
// more than half the disk and the rest keep working while it is flat out.
func suggestDiskIO(perf provider.DiskPerf, instances int) provider.DiskIO {
	busy := max(2, (instances+3)/4)
	share := func(total int) int { return roundDown(total / busy) }
	return provider.DiskIO{ReadMBps: share(perf.ReadMBps), WriteMBps: share(perf.WriteMBps), ReadIOPS: share(perf.ReadIOPS), WriteIOPS: share(perf.WriteIOPS)}
}

// roundDown keeps two significant digits (1234 -> 1200, 57 -> 55 in steps
// of 5 below 100) so suggestions read as chosen numbers.
func roundDown(value int) int {
	switch {
	case value >= 1000:
		step := 1
		for step*100 <= value {
			step *= 10
		}
		return value / step * step
	case value >= 100:
		return value / 10 * 10
	case value >= 10:
		return value / 5 * 5
	case value < 1:
		return 1
	}
	return value
}

// PlanStockCapacity computes the stock ceiling for plan (which may be a
// draft); excludeID is the plan's own id when it already exists.
func (s *CatalogStore) PlanStockCapacity(ctx context.Context, plan Plan, excludeID string) (StockCapacity, error) {
	return planStockCapacity(ctx, s.db, plan, excludeID)
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func planStockCapacity(ctx context.Context, db queryer, plan Plan, excludeID string) (StockCapacity, error) {
	var result StockCapacity
	// The nodes the plan sells on: its own node for a hosted plan, or every
	// platform node of its provider and virtualization.
	poolSQL := `SELECT n.id::text, n.name, n.disk_perf, coalesce(n.machine_id, n.id::text), n.capacity_vcpu, n.capacity_ram_mb, n.capacity_disk_gb,
		CASE WHEN coalesce(n.traffic_quota_gb,0)=0 THEN NULL ELSE
		  floor(n.traffic_quota_gb * least(n.overcommit_traffic, coalesce((SELECT max_overcommit_traffic FROM system_settings WHERE singleton=true), n.overcommit_traffic)))::bigint END
		FROM nodes n WHERE n.retired_at IS NULL AND `
	var args []any
	if plan.NodeID != "" {
		poolSQL += `n.id::text=$1`
		args = []any{plan.NodeID}
	} else {
		poolSQL += `n.owner_account_id IS NULL AND n.provider_type=$1 AND $2=ANY(n.virtualization_types)`
		args = []any{plan.ProviderType, plan.Virtualization}
	}
	rows, err := db.Query(ctx, poolSQL, args...)
	if err != nil {
		return result, err
	}
	type capacity struct{ vcpu, ram, disk int64 }
	groups := map[string]capacity{}
	perfs := map[string]provider.DiskPerf{}
	var unsupported []string
	var traffic int64
	trafficLimited := true
	for rows.Next() {
		var id, name, group string
		var c capacity
		var quota *int64
		var perf *provider.DiskPerf
		if err := rows.Scan(&id, &name, &perf, &group, &c.vcpu, &c.ram, &c.disk, &quota); err != nil {
			rows.Close()
			return result, err
		}
		result.Nodes++
		if perf != nil && perf.WriteMBps > 0 {
			perfs[group] = *perf
			if reason := perf.IOLimitErrors[plan.Virtualization]; reason != "" {
				unsupported = append(unsupported, name+"："+reason)
			}
		}
		// Nodes on one machine share its hardware; count it once.
		g := groups[group]
		groups[group] = capacity{max(g.vcpu, c.vcpu), max(g.ram, c.ram), max(g.disk, c.disk)}
		if quota == nil {
			trafficLimited = false
		} else {
			traffic += *quota
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	var total capacity
	groupKeys := make([]string, 0, len(groups))
	for key, c := range groups {
		total.vcpu, total.ram, total.disk = total.vcpu+c.vcpu, total.ram+c.ram, total.disk+c.disk
		groupKeys = append(groupKeys, key)
	}

	// Plans sharing these nodes: hosted plans on any node of the machines,
	// or the platform plans of the same provider.
	scope := `p.node_id::text IN (SELECT m.id::text FROM nodes m WHERE coalesce(m.machine_id, m.id::text) = ANY($1))`
	scopeArgs := []any{groupKeys}
	if plan.NodeID == "" {
		scope = `p.owner_account_id IS NULL AND p.provider_type=$1`
		scopeArgs = []any{plan.ProviderType}
	}
	excludeArg := len(scopeArgs) + 1
	var usedVCPU, usedRAM, usedDisk, usedTraffic int64
	if err := db.QueryRow(ctx, fmt.Sprintf(`
		WITH scoped AS (
			SELECT p.vcpu, p.ram_mb, p.disk_gb, p.traffic_gb,
			       CASE WHEN p.enabled THEN greatest(coalesce(p.stock_limit,0), %s) ELSE %s END AS units
			FROM plans p WHERE %s AND p.id::text <> $%d
		)
		SELECT coalesce(sum(units*vcpu),0)::bigint, coalesce(sum(units*ram_mb),0)::bigint, coalesce(sum(units*disk_gb),0)::bigint, coalesce(sum(units*traffic_gb),0)::bigint FROM scoped
	`, planHeldSQL, planHeldSQL, scope, excludeArg), append(scopeArgs, excludeID)...).Scan(&usedVCPU, &usedRAM, &usedDisk, &usedTraffic); err != nil {
		return result, err
	}
	// Instances of other plans already placed on these machines.
	var otherVCPU, otherRAM, otherDisk, otherTraffic int64
	if err := db.QueryRow(ctx, fmt.Sprintf(`
		SELECT coalesce(sum(r.vcpu),0)::bigint, coalesce(sum(r.ram_mb),0)::bigint, coalesce(sum(r.disk_gb),0)::bigint, coalesce(sum(p.traffic_gb),0)::bigint
		FROM inventory_reservations r JOIN services sv ON sv.id=r.service_id JOIN plans p ON p.id=sv.plan_id JOIN nodes rn ON rn.id=r.node_id
		WHERE r.status='reserved' AND coalesce(rn.machine_id, rn.id::text) = ANY($%d) AND NOT (%s) AND p.id::text <> $%d
	`, excludeArg+1, scope, excludeArg), append(append(scopeArgs, excludeID), groupKeys)...).Scan(&otherVCPU, &otherRAM, &otherDisk, &otherTraffic); err != nil {
		return result, err
	}
	if excludeID != "" {
		if err := db.QueryRow(ctx, `SELECT `+planHeldSQL+` FROM plans p WHERE p.id::text=$1`, excludeID).Scan(&result.Held); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return result, err
		}
	}
	result.FreeVCPU = total.vcpu - usedVCPU - otherVCPU
	result.FreeRAMMB = total.ram - usedRAM - otherRAM
	result.FreeDiskGB = total.disk - usedDisk - otherDisk
	limit := math.MaxInt
	fit := func(free, size int64) {
		if size <= 0 {
			return
		}
		units := int(max(free, 0) / size)
		limit = min(limit, units)
	}
	fit(result.FreeVCPU, int64(plan.VCPU))
	fit(result.FreeRAMMB, int64(plan.RAMMB))
	fit(result.FreeDiskGB, int64(plan.DiskGB))
	if trafficLimited && result.Nodes > 0 && plan.TrafficGB > 0 {
		free := traffic - usedTraffic - otherTraffic
		result.FreeTrafficGB = &free
		fit(free, int64(plan.TrafficGB))
	}
	if limit == math.MaxInt || result.Nodes == 0 {
		limit = 0
	}
	result.Max = limit
	// The weakest machine sets the suggestion, sized by how many of this
	// plan's instances it alone would hold.
	for group, perf := range perfs {
		c := groups[group]
		instances := math.MaxInt
		for _, pair := range [][2]int64{{c.vcpu, int64(plan.VCPU)}, {c.ram, int64(plan.RAMMB)}, {c.disk, int64(plan.DiskGB)}} {
			if pair[1] > 0 {
				instances = min(instances, int(pair[0]/pair[1]))
			}
		}
		if instances == math.MaxInt || instances < 1 {
			instances = 1
		}
		suggestion := suggestDiskIO(perf, instances)
		if result.DiskIO == nil || suggestion.WriteMBps < result.DiskIO.WriteMBps {
			result.DiskIO = &DiskIOSuggestion{DiskIO: suggestion, Host: perf, Instances: instances}
		}
	}
	if result.DiskIO != nil {
		result.DiskIO.Host.IOLimitErrors = nil
		result.DiskIO.Unsupported = unsupported
	}
	return result, nil
}

// ErrStockExceeded rejects a stock above what the nodes can hold.
var ErrStockExceeded = errors.New("stock exceeds sellable capacity")

// checkPlanLimits enforces a plan's stock and the cycle's purchase limit for
// an order of quantity units; the plan row is locked so concurrent orders
// cannot both take the last unit.
func checkPlanLimits(ctx context.Context, tx pgx.Tx, planID, cycle string, quantity int) error {
	var stock *int
	var held int
	if err := tx.QueryRow(ctx, `SELECT p.stock_limit, `+planHeldSQL+` FROM plans p WHERE p.id=$1 FOR UPDATE OF p`, planID).Scan(&stock, &held); err != nil {
		return err
	}
	if stock != nil && held+quantity > *stock {
		if left := *stock - held; left > 0 {
			return &HostedOrderError{fmt.Sprintf("库存不足，该套餐仅剩 %d 台", left)}
		}
		return &HostedOrderError{"该套餐已售罄"}
	}
	var limit *int
	var sold int
	err := tx.QueryRow(ctx, `SELECT pp.purchase_limit, `+priceSoldSQL+` FROM plan_prices pp
		WHERE pp.plan_id=$1 AND pp.billing_cycle=$2 AND pp.active_from<=now() AND (pp.active_until IS NULL OR pp.active_until>now())
		ORDER BY pp.active_from DESC LIMIT 1`, planID, cycle).Scan(&limit, &sold)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if limit != nil && sold+quantity > *limit {
		name := BillingCycleName(cycle)
		if left := *limit - sold; left > 0 {
			return &HostedOrderError{fmt.Sprintf("%s价格限购 %d 次，仅剩 %d 次", name, *limit, left)}
		}
		return &HostedOrderError{fmt.Sprintf("%s价格已达限购次数，请选择其他计费周期", name)}
	}
	return nil
}

// PlanStockLimit returns a saved plan's stock (nil when unset or the plan
// does not exist).
func (s *CatalogStore) PlanStockLimit(ctx context.Context, id string) (*int, error) {
	var stock *int
	err := s.db.QueryRow(ctx, `SELECT stock_limit FROM plans WHERE id::text=$1`, id).Scan(&stock)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return stock, err
}
