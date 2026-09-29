package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"vpsbill/internal/security"
)

// maxPendingAgents caps how many unclaimed agents one owner may keep
// connected, since each holds a connection open.
const maxPendingAgents = 50

var ErrEnrollmentNotFound = errors.New("pending agent not found")

// PendingAgent is an agent that connected with an owner's enroll key and is
// waiting to be added as a node.
type PendingAgent struct {
	ID           string    `json:"id"`
	Endpoint     string    `json:"-"`
	Hostname     string    `json:"hostname"`
	AgentVersion string    `json:"agent_version"`
	Runtimes     []string  `json:"runtimes"`
	RemoteIP     string    `json:"remote_ip"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// EnrollKey returns the owner's enroll key for install commands, creating
// it on first use. An empty accountID is the platform.
func (s *CatalogStore) EnrollKey(ctx context.Context, accountID string) (string, error) {
	token, _, err := security.NewToken()
	if err != nil {
		return "", err
	}
	// A leading - would read as a flag on the agent command line.
	token = "k" + strings.TrimLeft(token, "-_")[:31]
	var key string
	if accountID == "" {
		err = s.db.QueryRow(ctx, `UPDATE system_settings SET agent_enroll_key=coalesce(agent_enroll_key,$1) WHERE singleton RETURNING agent_enroll_key`, token).Scan(&key)
	} else {
		err = s.db.QueryRow(ctx, `UPDATE accounts SET agent_enroll_key=coalesce(agent_enroll_key,$2) WHERE id=$1 RETURNING agent_enroll_key`, accountID, token).Scan(&key)
	}
	return key, err
}

// Enroll records a connected agent under the owner of key and reports
// whether the key belongs to anyone. A known agent reconnecting refreshes
// its row; a new one is refused once the owner has maxPendingAgents.
func (s *CatalogStore) Enroll(ctx context.Context, key, endpoint string, tokenCiphertext []byte, hostname, version string, runtimes []string, remoteIP string) (bool, error) {
	if key == "" {
		return false, nil
	}
	var accountID *string
	err := s.db.QueryRow(ctx, `SELECT id::text FROM accounts WHERE agent_enroll_key=$1`, key).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		var platform bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_settings WHERE singleton AND agent_enroll_key=$1)`, key).Scan(&platform); err != nil || !platform {
			return false, err
		}
		accountID, err = nil, nil
	}
	if err != nil {
		return false, err
	}
	if runtimes == nil {
		runtimes = []string{}
	}
	command, err := s.db.Exec(ctx, `
		INSERT INTO agent_enrollments(endpoint, account_id, token_ciphertext, hostname, agent_version, runtimes, remote_ip)
		SELECT $1, $2::uuid, $3, left($4,120), left($5,40), $6, left($7,64)
		WHERE EXISTS(SELECT 1 FROM agent_enrollments WHERE endpoint=$1)
		   OR (SELECT count(*) FROM agent_enrollments WHERE account_id IS NOT DISTINCT FROM $2::uuid) < $8
		ON CONFLICT(endpoint) DO UPDATE SET account_id=excluded.account_id, token_ciphertext=excluded.token_ciphertext,
		    hostname=excluded.hostname, agent_version=excluded.agent_version, runtimes=excluded.runtimes,
		    remote_ip=excluded.remote_ip, last_seen_at=now()
	`, endpoint, accountID, tokenCiphertext, hostname, version, runtimes, remoteIP, maxPendingAgents)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

// PendingAgents lists the owner's agents not yet added as nodes, newest
// first. An empty accountID is the platform.
func (s *CatalogStore) PendingAgents(ctx context.Context, accountID string) ([]PendingAgent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT e.id::text, e.endpoint, e.hostname, e.agent_version, e.runtimes, e.remote_ip, e.first_seen_at, e.last_seen_at
		FROM agent_enrollments e
		WHERE e.account_id IS NOT DISTINCT FROM nullif($1,'')::uuid
		  AND NOT EXISTS(SELECT 1 FROM nodes n WHERE n.base_url=e.endpoint)
		ORDER BY e.last_seen_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := []PendingAgent{}
	for rows.Next() {
		var agent PendingAgent
		if err := rows.Scan(&agent.ID, &agent.Endpoint, &agent.Hostname, &agent.AgentVersion, &agent.Runtimes, &agent.RemoteIP, &agent.FirstSeenAt, &agent.LastSeenAt); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

// EnrollmentToken returns the sealed agent token of the owner's pending
// agent, for adding it as a node.
func (s *CatalogStore) EnrollmentToken(ctx context.Context, id, accountID string) ([]byte, error) {
	var ciphertext []byte
	err := s.db.QueryRow(ctx, `SELECT token_ciphertext FROM agent_enrollments WHERE id::text=$1 AND account_id IS NOT DISTINCT FROM nullif($2,'')::uuid`, id, accountID).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEnrollmentNotFound
	}
	return ciphertext, err
}

// DismissEnrollment forgets the owner's pending agent; it is listed again
// if it reconnects with the key.
func (s *CatalogStore) DismissEnrollment(ctx context.Context, id, accountID string) error {
	command, err := s.db.Exec(ctx, `DELETE FROM agent_enrollments WHERE id::text=$1 AND account_id IS NOT DISTINCT FROM nullif($2,'')::uuid`, id, accountID)
	if err == nil && command.RowsAffected() == 0 {
		return ErrEnrollmentNotFound
	}
	return err
}

// ClearEnrollment removes the pending entry of an agent now added as a node.
func (s *CatalogStore) ClearEnrollment(ctx context.Context, endpoint string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM agent_enrollments WHERE endpoint=$1`, endpoint)
	return err
}

var ErrRegionDisabled = errors.New("region disabled")

// Region is a sales region with its node count, for administration.
type Region struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Nodes   int    `json:"nodes"`
}

// AllRegions lists every region, disabled ones included.
func (s *CatalogStore) AllRegions(ctx context.Context) ([]Region, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id::text, r.code, r.name, r.enabled, (SELECT count(*) FROM nodes n WHERE n.region_id=r.id AND n.retired_at IS NULL)::int
		FROM regions r ORDER BY r.enabled DESC, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	regions := []Region{}
	for rows.Next() {
		var region Region
		if err := rows.Scan(&region.ID, &region.Code, &region.Name, &region.Enabled, &region.Nodes); err != nil {
			return nil, err
		}
		regions = append(regions, region)
	}
	return regions, rows.Err()
}

// UpdateRegion renames a region or switches it on or off.
func (s *CatalogStore) UpdateRegion(ctx context.Context, id, name string, enabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return fmt.Errorf("%w: 地域名称需为 1-40 个字符", ErrRegionInvalid)
	}
	command, err := s.db.Exec(ctx, `UPDATE regions SET name=$2, enabled=$3 WHERE id::text=$1`, id, name, enabled)
	if err == nil && command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

var ErrRegionInvalid = errors.New("invalid region")

var regionCodeChars = regexp.MustCompile(`[^A-Z0-9]+`)

// EnsureRegion finds a region by name (ignoring case) or creates it with a
// code derived from the name, so hosts and administrators can type a new
// region instead of needing one set up first. A disabled region is refused.
func (s *CatalogStore) EnsureRegion(ctx context.Context, name string) (RegionSummary, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return RegionSummary{}, fmt.Errorf("%w: 地域名称需为 1-40 个字符", ErrRegionInvalid)
	}
	var region RegionSummary
	var enabled bool
	err := s.db.QueryRow(ctx, `SELECT id::text, code, name, enabled FROM regions WHERE lower(name)=lower($1) ORDER BY enabled DESC LIMIT 1`, name).
		Scan(&region.ID, &region.Code, &region.Name, &enabled)
	if err == nil {
		if !enabled {
			return RegionSummary{}, ErrRegionDisabled
		}
		return region, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RegionSummary{}, err
	}
	code := regionCodeChars.ReplaceAllString(strings.ToUpper(name), "")
	if len(code) > 12 {
		code = code[:12]
	}
	for attempt := 0; attempt < 5; attempt++ {
		candidate := code
		if len(candidate) < 2 || attempt > 0 {
			suffix := make([]byte, 2)
			_, _ = rand.Read(suffix)
			candidate = "R" + strings.ToUpper(hex.EncodeToString(suffix))
			if len(code) >= 2 {
				candidate = code + "-" + strings.ToUpper(hex.EncodeToString(suffix))
			}
		}
		err = s.db.QueryRow(ctx, `INSERT INTO regions(code, name) VALUES($1, $2) ON CONFLICT(code) DO NOTHING RETURNING id::text, code, name`, candidate, name).
			Scan(&region.ID, &region.Code, &region.Name)
		if err == nil {
			return region, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return RegionSummary{}, err
		}
	}
	return RegionSummary{}, errors.New("could not pick a region code")
}
