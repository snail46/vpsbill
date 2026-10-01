package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNameTaken rejects a category or preset name already in use.
var ErrNameTaken = errors.New("name already in use")

// ErrNotFound reports a category or preset that does not exist.
var ErrNotFound = errors.New("not found")

// PlanCategory groups platform plans in the admin catalog and the shop.
type PlanCategory struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int       `json:"sort_order"`
	Plans       int       `json:"plans"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListPlanCategories returns categories in display order with how many
// platform plans each holds.
func (s *CatalogStore) ListPlanCategories(ctx context.Context) ([]PlanCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.name, c.description, c.sort_order,
		       (SELECT count(*) FROM plans p WHERE p.category_id=c.id AND p.owner_account_id IS NULL)::int, c.created_at
		FROM plan_categories c ORDER BY c.sort_order, c.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	categories := make([]PlanCategory, 0)
	for rows.Next() {
		var item PlanCategory
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.SortOrder, &item.Plans, &item.CreatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, item)
	}
	return categories, rows.Err()
}

// SavePlanCategory creates a category (empty id) or updates one.
func (s *CatalogStore) SavePlanCategory(ctx context.Context, id string, input PlanCategory) (PlanCategory, error) {
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	var err error
	if id == "" {
		err = s.db.QueryRow(ctx, `
			INSERT INTO plan_categories(name, description, sort_order) VALUES($1, $2, $3)
			RETURNING id, created_at
		`, input.Name, input.Description, input.SortOrder).Scan(&input.ID, &input.CreatedAt)
	} else {
		err = s.db.QueryRow(ctx, `
			UPDATE plan_categories SET name=$2, description=$3, sort_order=$4, updated_at=now() WHERE id::text=$1
			RETURNING id, created_at, (SELECT count(*) FROM plans p WHERE p.category_id=plan_categories.id AND p.owner_account_id IS NULL)::int
		`, id, input.Name, input.Description, input.SortOrder).Scan(&input.ID, &input.CreatedAt, &input.Plans)
	}
	return input, catalogNameError(err)
}

// DeletePlanCategory removes a category; its plans become uncategorised.
func (s *CatalogStore) DeletePlanCategory(ctx context.Context, id string) error {
	command, err := s.db.Exec(ctx, `DELETE FROM plan_categories WHERE id::text=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PlanPreset is a named set of plan settings the admin form fills in from.
type PlanPreset struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Settings  json.RawMessage `json:"settings"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *CatalogStore) ListPlanPresets(ctx context.Context) ([]PlanPreset, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, settings, updated_at FROM plan_presets ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	presets := make([]PlanPreset, 0)
	for rows.Next() {
		var item PlanPreset
		if err := rows.Scan(&item.ID, &item.Name, &item.Settings, &item.UpdatedAt); err != nil {
			return nil, err
		}
		presets = append(presets, item)
	}
	return presets, rows.Err()
}

// SavePlanPreset creates a preset (empty id) or replaces one.
func (s *CatalogStore) SavePlanPreset(ctx context.Context, id string, input PlanPreset) (PlanPreset, error) {
	input.Name = strings.TrimSpace(input.Name)
	var err error
	if id == "" {
		err = s.db.QueryRow(ctx, `INSERT INTO plan_presets(name, settings) VALUES($1, $2) RETURNING id, updated_at`,
			input.Name, input.Settings).Scan(&input.ID, &input.UpdatedAt)
	} else {
		err = s.db.QueryRow(ctx, `UPDATE plan_presets SET name=$2, settings=$3, updated_at=now() WHERE id::text=$1 RETURNING id, updated_at`,
			id, input.Name, input.Settings).Scan(&input.ID, &input.UpdatedAt)
	}
	return input, catalogNameError(err)
}

func (s *CatalogStore) DeletePlanPreset(ctx context.Context, id string) error {
	command, err := s.db.Exec(ctx, `DELETE FROM plan_presets WHERE id::text=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func catalogNameError(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrNameTaken
	}
	return err
}
