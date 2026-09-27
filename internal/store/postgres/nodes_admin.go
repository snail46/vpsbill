package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ErrNodeNotFound is returned for an unknown node id.
var ErrNodeNotFound = errors.New("node not found")

// ErrNodeInUse is returned when deleting a node that still hosts live services.
var ErrNodeInUse = errors.New("node still hosts services")

// UpdateNode holds editable node settings. A nil APIKeyCiphertext keeps the
// stored credential.
type UpdateNode struct {
	Name                string
	BaseURL             string
	APIKeyCiphertext    []byte
	ProviderOptions     json.RawMessage
	VirtualizationTypes []string
	Capacity            map[string]any
	CapacityVCPU        int
	CapacityRAMMB       int64
	CapacityDiskGB      int64
}

func (s *CatalogStore) UpdateNode(ctx context.Context, id string, input UpdateNode) error {
	capacity, _ := json.Marshal(input.Capacity)
	command, err := s.db.Exec(ctx, `
		UPDATE nodes SET name=$2, base_url=$3, api_key_ciphertext=coalesce($4, api_key_ciphertext),
		    provider_options=coalesce($5::jsonb, '{}'::jsonb), virtualization_types=$6, capacity=$7,
		    capacity_vcpu=$8, capacity_ram_mb=$9, capacity_disk_gb=$10, status='online', last_seen_at=now(), updated_at=now()
		WHERE id=$1
	`, id, strings.TrimSpace(input.Name), strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), input.APIKeyCiphertext,
		nullableJSON(input.ProviderOptions), input.VirtualizationTypes, capacity, input.CapacityVCPU, input.CapacityRAMMB, input.CapacityDiskGB)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// DeleteNode removes a node that no longer hosts any live service, together
// with its released reservation history. Terminated services keep their
// records but are detached from the node.
func (s *CatalogStore) DeleteNode(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inUse bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM services WHERE node_id=$1 AND status<>'terminated')
		    OR EXISTS(SELECT 1 FROM inventory_reservations WHERE node_id=$1 AND status='reserved')
	`, id).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return ErrNodeInUse
	}
	if _, err := tx.Exec(ctx, `UPDATE services SET node_id=NULL, updated_at=now() WHERE node_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM inventory_reservations WHERE node_id=$1`, id); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNodeNotFound
	}
	return tx.Commit(ctx)
}
