package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnnouncementStore keeps the notices staff publish on the customer
// overview.
type AnnouncementStore struct{ db *pgxpool.Pool }

func NewAnnouncementStore(db *pgxpool.Pool) *AnnouncementStore { return &AnnouncementStore{db: db} }

type Announcement struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Pinned    bool      `json:"pinned"`
	Published bool      `json:"published"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	ErrAnnouncementNotFound = errors.New("announcement not found")
	ErrAnnouncementInvalid  = errors.New("announcement title must be 1-120 characters and body at most 4000")
)

// List returns announcements, pinned first then newest; publishedOnly is
// the customer view.
func (s *AnnouncementStore) List(ctx context.Context, publishedOnly bool, limit int) ([]Announcement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id,title,body,pinned,published,created_at,updated_at FROM announcements
		WHERE published OR NOT $1 ORDER BY pinned DESC, created_at DESC LIMIT $2
	`, publishedOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Announcement, 0)
	for rows.Next() {
		var item Announcement
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Pinned, &item.Published, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (in *Announcement) normalize() error {
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	if n := len([]rune(in.Title)); n == 0 || n > 120 || len([]rune(in.Body)) > 4000 {
		return ErrAnnouncementInvalid
	}
	return nil
}

// Save creates the announcement when id is empty and updates it otherwise.
func (s *AnnouncementStore) Save(ctx context.Context, id string, in Announcement) (Announcement, error) {
	if err := in.normalize(); err != nil {
		return Announcement{}, err
	}
	var err error
	if id == "" {
		err = s.db.QueryRow(ctx, `INSERT INTO announcements(title,body,pinned,published) VALUES($1,$2,$3,$4) RETURNING id,created_at,updated_at`,
			in.Title, in.Body, in.Pinned, in.Published).Scan(&in.ID, &in.CreatedAt, &in.UpdatedAt)
	} else {
		err = s.db.QueryRow(ctx, `UPDATE announcements SET title=$2,body=$3,pinned=$4,published=$5,updated_at=now() WHERE id=$1 RETURNING id,created_at,updated_at`,
			id, in.Title, in.Body, in.Pinned, in.Published).Scan(&in.ID, &in.CreatedAt, &in.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Announcement{}, ErrAnnouncementNotFound
	}
	return in, err
}

func (s *AnnouncementStore) Delete(ctx context.Context, id string) error {
	command, err := s.db.Exec(ctx, `DELETE FROM announcements WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrAnnouncementNotFound
	}
	return nil
}

// SetAutoRenew switches renewal from the balance for one of the customer's
// instances.
func (s *PortalStore) SetAutoRenew(ctx context.Context, accountID, serviceID string, enabled bool) error {
	command, err := s.db.Exec(ctx, `UPDATE services SET auto_renew=$3,updated_at=now() WHERE id=$1 AND account_id=$2 AND status NOT IN ('terminating','terminated')`,
		serviceID, accountID, enabled)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrServiceNotFound
	}
	return nil
}

// SetServiceTemplate records the system a reinstall put on an instance.
func (s *PortalStore) SetServiceTemplate(ctx context.Context, accountID, serviceID, templateID string) error {
	_, err := s.db.Exec(ctx, `UPDATE services SET template_id=$3,updated_at=now() WHERE id=$1 AND account_id=$2`, serviceID, accountID, templateID)
	return err
}

// HostEarnings sums what a host's nodes hold in escrow and have paid out.
type HostEarnings struct {
	Nodes         int   `json:"nodes"`
	PendingMinor  int64 `json:"pending_minor"`
	ReleasedMinor int64 `json:"released_minor"`
}

func (m *MarketplaceStore) HostEarnings(ctx context.Context, accountID string) (HostEarnings, error) {
	var result HostEarnings
	err := m.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM nodes WHERE owner_account_id=$1 AND retired_at IS NULL)::int,
		       coalesce(sum(CASE WHEN status='holding' THEN host_share_minor-released_host_minor ELSE 0 END),0)::bigint,
		       coalesce(sum(released_host_minor),0)::bigint
		FROM marketplace_escrows WHERE host_account_id=$1
	`, accountID).Scan(&result.Nodes, &result.PendingMinor, &result.ReleasedMinor)
	return result, err
}
