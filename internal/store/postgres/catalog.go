package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogStore struct {
	db *pgxpool.Pool
}

func NewCatalogStore(db *pgxpool.Pool) *CatalogStore {
	return &CatalogStore{db: db}
}

type Node struct {
	ID         string `json:"id"`
	RegionID   string `json:"region_id"`
	RegionCode string `json:"region_code"`
	RegionName string `json:"region_name"`
	Name       string `json:"name"`
	NodeEndpoint
	Status              string         `json:"status"`
	VirtualizationTypes []string       `json:"virtualization_types"`
	Capacity            map[string]any `json:"capacity"`
	CapacityVCPU        int            `json:"capacity_vcpu"`
	CapacityRAMMB       int64          `json:"capacity_ram_mb"`
	CapacityDiskGB      int64          `json:"capacity_disk_gb"`
	LastSeenAt          *time.Time     `json:"last_seen_at"`
	CreatedAt           time.Time      `json:"created_at"`
}

type CreateNode struct {
	RegionCode          string
	RegionName          string
	Name                string
	ProviderType        string
	BaseURL             string
	APIKeyCiphertext    []byte
	ProviderOptions     json.RawMessage
	VirtualizationTypes []string
	Capacity            map[string]any
	CapacityVCPU        int
	CapacityRAMMB       int64
	CapacityDiskGB      int64
}

func (s *CatalogStore) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.Query(ctx, `
		SELECT n.id, n.region_id, r.code, r.name, n.name, n.provider_type, n.base_url, n.provider_options, n.status,
		       n.virtualization_types, n.capacity, n.capacity_vcpu, n.capacity_ram_mb,
		       n.capacity_disk_gb, n.last_seen_at, n.created_at
		FROM nodes n JOIN regions r ON r.id=n.region_id
		ORDER BY r.code, n.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]Node, 0)
	for rows.Next() {
		var node Node
		var capacity []byte
		if err := rows.Scan(&node.ID, &node.RegionID, &node.RegionCode, &node.RegionName, &node.Name, &node.ProviderType, &node.BaseURL, &node.ProviderOptions, &node.Status, &node.VirtualizationTypes, &capacity, &node.CapacityVCPU, &node.CapacityRAMMB, &node.CapacityDiskGB, &node.LastSeenAt, &node.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(capacity, &node.Capacity)
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *CatalogStore) CreateNode(ctx context.Context, input CreateNode) (Node, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Node{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var regionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO regions(code, name) VALUES(upper($1), $2)
		ON CONFLICT(code) DO UPDATE SET name=excluded.name
		RETURNING id
	`, strings.TrimSpace(input.RegionCode), strings.TrimSpace(input.RegionName)).Scan(&regionID); err != nil {
		return Node{}, fmt.Errorf("upsert region: %w", err)
	}
	capacity, _ := json.Marshal(input.Capacity)
	var node Node
	if err := tx.QueryRow(ctx, `
		INSERT INTO nodes(region_id, name, provider_type, base_url, api_key_ciphertext, status, virtualization_types, capacity,
		                  capacity_vcpu, capacity_ram_mb, capacity_disk_gb, last_seen_at, provider_options)
		VALUES($1, $2, $3, $4, $5, 'online', $6, $7, $8, $9, $10, now(), coalesce($11::jsonb, '{}'::jsonb))
		RETURNING id, status, last_seen_at, created_at
	`, regionID, strings.TrimSpace(input.Name), input.ProviderType, strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), input.APIKeyCiphertext, input.VirtualizationTypes, capacity, input.CapacityVCPU, input.CapacityRAMMB, input.CapacityDiskGB, nullableJSON(input.ProviderOptions)).Scan(&node.ID, &node.Status, &node.LastSeenAt, &node.CreatedAt); err != nil {
		return Node{}, fmt.Errorf("create node: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Node{}, err
	}
	node.RegionID = regionID
	node.RegionCode = strings.ToUpper(strings.TrimSpace(input.RegionCode))
	node.RegionName = strings.TrimSpace(input.RegionName)
	node.Name = strings.TrimSpace(input.Name)
	node.ProviderType = input.ProviderType
	node.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	node.ProviderOptions = input.ProviderOptions
	node.VirtualizationTypes = input.VirtualizationTypes
	node.Capacity = input.Capacity
	node.CapacityVCPU = input.CapacityVCPU
	node.CapacityRAMMB = input.CapacityRAMMB
	node.CapacityDiskGB = input.CapacityDiskGB
	return node, nil
}

func (s *CatalogStore) NodeSecret(ctx context.Context, id string) (Node, error) {
	var node Node
	var capacity []byte
	err := s.db.QueryRow(ctx, `
		SELECT n.id, n.region_id, r.code, r.name, n.name, n.provider_type, n.base_url, n.api_key_ciphertext,n.provider_options,
		       n.status, n.virtualization_types, n.capacity, n.capacity_vcpu, n.capacity_ram_mb,
		       n.capacity_disk_gb, n.last_seen_at, n.created_at
		FROM nodes n JOIN regions r ON r.id=n.region_id WHERE n.id=$1
	`, id).Scan(&node.ID, &node.RegionID, &node.RegionCode, &node.RegionName, &node.Name, &node.ProviderType, &node.BaseURL, &node.APIKeyCiphertext, &node.ProviderOptions, &node.Status, &node.VirtualizationTypes, &capacity, &node.CapacityVCPU, &node.CapacityRAMMB, &node.CapacityDiskGB, &node.LastSeenAt, &node.CreatedAt)
	_ = json.Unmarshal(capacity, &node.Capacity)
	return node, err
}

func (s *CatalogStore) ListNodeSecrets(ctx context.Context) ([]Node, error) {
	rows, err := s.db.Query(ctx, `
		SELECT n.id,n.region_id,r.code,r.name,n.name,n.provider_type,n.base_url,n.api_key_ciphertext,n.provider_options,n.status,
		       n.virtualization_types,n.capacity,n.capacity_vcpu,n.capacity_ram_mb,n.capacity_disk_gb,n.last_seen_at,n.created_at
		FROM nodes n JOIN regions r ON r.id=n.region_id ORDER BY n.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Node, 0)
	for rows.Next() {
		var node Node
		var capacity []byte
		if err := rows.Scan(&node.ID, &node.RegionID, &node.RegionCode, &node.RegionName, &node.Name, &node.ProviderType, &node.BaseURL, &node.APIKeyCiphertext, &node.ProviderOptions,
			&node.Status, &node.VirtualizationTypes, &capacity, &node.CapacityVCPU, &node.CapacityRAMMB, &node.CapacityDiskGB, &node.LastSeenAt, &node.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(capacity, &node.Capacity)
		result = append(result, node)
	}
	return result, rows.Err()
}

func (s *CatalogStore) UpdateNodeHealth(ctx context.Context, id, status string, capacity map[string]any, totals ...int64) error {
	body, _ := json.Marshal(capacity)
	var vcpu, ramMB, diskGB int64
	if len(totals) >= 3 {
		vcpu, ramMB, diskGB = totals[0], totals[1], totals[2]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE nodes SET status=$2, capacity=$3,
		    capacity_vcpu=CASE WHEN $2='online' THEN $4 ELSE capacity_vcpu END,
		    capacity_ram_mb=CASE WHEN $2='online' THEN $5 ELSE capacity_ram_mb END,
		    capacity_disk_gb=CASE WHEN $2='online' THEN $6 ELSE capacity_disk_gb END,
		    last_seen_at=CASE WHEN $2='online' THEN now() ELSE last_seen_at END, updated_at=now()
		WHERE id=$1
	`, id, status, body, vcpu, ramMB, diskGB)
	return err
}

type Price struct {
	ID            string     `json:"id"`
	Currency      string     `json:"currency"`
	BillingCycle  string     `json:"billing_cycle"`
	AmountMinor   int64      `json:"amount_minor"`
	SetupFeeMinor int64      `json:"setup_fee_minor"`
	ActiveFrom    time.Time  `json:"active_from"`
	ActiveUntil   *time.Time `json:"active_until"`
}

type Plan struct {
	ID                 string    `json:"id"`
	Code               string    `json:"code"`
	Name               string    `json:"name"`
	ProviderType       string    `json:"provider_type"`
	Virtualization     string    `json:"virtualization"`
	VCPU               int       `json:"vcpu"`
	RAMMB              int       `json:"ram_mb"`
	DiskGB             int       `json:"disk_gb"`
	TrafficGB          int       `json:"traffic_gb"`
	NetworkDownMbps    int       `json:"network_down_mbps"`
	NetworkUpMbps      int       `json:"network_up_mbps"`
	SnapshotLimit      int       `json:"snapshot_limit"`
	AssignNAT          bool      `json:"assign_nat"`
	PortMappingCount   int       `json:"port_mapping_count"`
	AssignIPv4         bool      `json:"assign_ipv4"`
	IPv4Count          int       `json:"ipv4_count"`
	AssignIPv6         bool      `json:"assign_ipv6"`
	IPv6Count          int       `json:"ipv6_count"`
	DefaultTemplateID  string    `json:"default_template_id"`
	AllowedTemplateIDs []string  `json:"allowed_template_ids"`
	Enabled            bool      `json:"enabled"`
	Version            int       `json:"version"`
	Prices             []Price   `json:"prices"`
	CreatedAt          time.Time `json:"created_at"`
}

func (s *CatalogStore) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, code, name, provider_type, virtualization, vcpu, ram_mb, disk_gb, traffic_gb,
		       network_down_mbps, network_up_mbps, snapshot_limit,
		       assign_nat, port_mapping_count, assign_ipv4, ipv4_count, assign_ipv6, ipv6_count,
		       default_template_id, allowed_template_ids, enabled, version, created_at
		FROM plans ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]Plan, 0)
	for rows.Next() {
		var plan Plan
		if err := rows.Scan(&plan.ID, &plan.Code, &plan.Name, &plan.ProviderType, &plan.Virtualization, &plan.VCPU, &plan.RAMMB, &plan.DiskGB, &plan.TrafficGB, &plan.NetworkDownMbps, &plan.NetworkUpMbps, &plan.SnapshotLimit,
			&plan.AssignNAT, &plan.PortMappingCount, &plan.AssignIPv4, &plan.IPv4Count, &plan.AssignIPv6, &plan.IPv6Count,
			&plan.DefaultTemplateID, &plan.AllowedTemplateIDs, &plan.Enabled, &plan.Version, &plan.CreatedAt); err != nil {
			return nil, err
		}
		plan.Prices = []Price{}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range plans {
		priceRows, err := s.db.Query(ctx, `
			SELECT id, currency, billing_cycle, amount_minor, setup_fee_minor, active_from, active_until
			FROM plan_prices WHERE plan_id=$1 AND (active_until IS NULL OR active_until > now())
			ORDER BY currency, billing_cycle
		`, plans[i].ID)
		if err != nil {
			return nil, err
		}
		for priceRows.Next() {
			var price Price
			if err := priceRows.Scan(&price.ID, &price.Currency, &price.BillingCycle, &price.AmountMinor, &price.SetupFeeMinor, &price.ActiveFrom, &price.ActiveUntil); err != nil {
				priceRows.Close()
				return nil, err
			}
			plans[i].Prices = append(plans[i].Prices, price)
		}
		priceRows.Close()
	}
	return plans, nil
}

func (s *CatalogStore) CreatePlan(ctx context.Context, input Plan) (Plan, error) {
	input = withPlanNetworkDefaults(input)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO plans(code, name, virtualization, vcpu, ram_mb, disk_gb, traffic_gb,
		                  network_down_mbps, network_up_mbps, snapshot_limit,
		                  assign_nat, port_mapping_count, assign_ipv4, ipv4_count, assign_ipv6, ipv6_count,
		                  default_template_id, allowed_template_ids, enabled, provider_type)
		VALUES(upper($1), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING id, code, version, created_at
	`, input.Code, input.Name, input.Virtualization, input.VCPU, input.RAMMB, input.DiskGB, input.TrafficGB, input.NetworkDownMbps, input.NetworkUpMbps, input.SnapshotLimit,
		input.AssignNAT, input.PortMappingCount, input.AssignIPv4, input.IPv4Count, input.AssignIPv6, input.IPv6Count,
		input.DefaultTemplateID, input.AllowedTemplateIDs, input.Enabled, input.ProviderType).Scan(&input.ID, &input.Code, &input.Version, &input.CreatedAt); err != nil {
		return Plan{}, fmt.Errorf("create plan: %w", err)
	}
	for i := range input.Prices {
		input.Prices[i].Currency = strings.ToUpper(strings.TrimSpace(input.Prices[i].Currency))
		if err := tx.QueryRow(ctx, `
			INSERT INTO plan_prices(plan_id, currency, billing_cycle, amount_minor, setup_fee_minor)
			VALUES($1, $2, $3, $4, $5)
			RETURNING id, active_from
		`, input.ID, input.Prices[i].Currency, input.Prices[i].BillingCycle, input.Prices[i].AmountMinor, input.Prices[i].SetupFeeMinor).Scan(&input.Prices[i].ID, &input.Prices[i].ActiveFrom); err != nil {
			return Plan{}, fmt.Errorf("create plan price: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Plan{}, err
	}
	return input, nil
}

func (s *CatalogStore) UpdatePlan(ctx context.Context, id string, input Plan) (Plan, error) {
	input = withPlanNetworkDefaults(input)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		UPDATE plans SET code=upper($2), name=$3, virtualization=$4, vcpu=$5, ram_mb=$6, disk_gb=$7,
		       traffic_gb=$8, network_down_mbps=$9, network_up_mbps=$10, snapshot_limit=$11,
		       assign_nat=$12, port_mapping_count=$13, assign_ipv4=$14, ipv4_count=$15,
		       assign_ipv6=$16, ipv6_count=$17, default_template_id=$18, allowed_template_ids=$19,
		       enabled=$20, provider_type=$21, version=version+1, updated_at=now()
		WHERE id=$1
		RETURNING id, code, version, created_at
	`, id, input.Code, input.Name, input.Virtualization, input.VCPU, input.RAMMB, input.DiskGB, input.TrafficGB,
		input.NetworkDownMbps, input.NetworkUpMbps, input.SnapshotLimit, input.AssignNAT, input.PortMappingCount,
		input.AssignIPv4, input.IPv4Count, input.AssignIPv6, input.IPv6Count, input.DefaultTemplateID,
		input.AllowedTemplateIDs, input.Enabled, input.ProviderType).Scan(&input.ID, &input.Code, &input.Version, &input.CreatedAt); err != nil {
		return Plan{}, fmt.Errorf("update plan: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE plan_prices SET active_until=$2 WHERE plan_id=$1 AND active_until IS NULL`, id, now); err != nil {
		return Plan{}, fmt.Errorf("expire plan prices: %w", err)
	}
	for i := range input.Prices {
		input.Prices[i].Currency = strings.ToUpper(strings.TrimSpace(input.Prices[i].Currency))
		if err := tx.QueryRow(ctx, `
			INSERT INTO plan_prices(plan_id, currency, billing_cycle, amount_minor, setup_fee_minor, active_from)
			VALUES($1, $2, $3, $4, $5, $6)
			RETURNING id, active_from
		`, id, input.Prices[i].Currency, input.Prices[i].BillingCycle, input.Prices[i].AmountMinor,
			input.Prices[i].SetupFeeMinor, now).Scan(&input.Prices[i].ID, &input.Prices[i].ActiveFrom); err != nil {
			return Plan{}, fmt.Errorf("update plan price: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Plan{}, err
	}
	return input, nil
}

func withPlanNetworkDefaults(input Plan) Plan {
	if input.IPv4Count < 1 {
		input.IPv4Count = 1
	}
	if input.IPv6Count < 1 {
		input.IPv6Count = 1
	}
	if !input.AssignNAT && !input.AssignIPv4 && !input.AssignIPv6 {
		input.AssignNAT = true
		input.AssignIPv6 = true
	}
	return input
}

func (s *CatalogStore) SetPlanEnabled(ctx context.Context, id string, enabled bool) error {
	command, err := s.db.Exec(ctx, "UPDATE plans SET enabled=$2, version=version+1, updated_at=now() WHERE id=$1", id, enabled)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("plan not found")
	}
	return nil
}

// NodeExistsByEndpoint reports whether a node is registered with the given
// provider base URL (for agent-managed nodes, the agent endpoint).
func (s *CatalogStore) NodeExistsByEndpoint(ctx context.Context, baseURL string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM nodes WHERE base_url=$1)", baseURL).Scan(&exists)
	return err == nil && exists
}
