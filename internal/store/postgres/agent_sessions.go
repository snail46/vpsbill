package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// agentSessionTTL is how long a holder may miss heartbeats before other
// instances stop forwarding to it.
const agentSessionTTL = 90 * time.Second

// AgentDirectory records which API instance holds each agent connection.
type AgentDirectory struct {
	db *pgxpool.Pool
}

func NewAgentDirectory(db *pgxpool.Pool) *AgentDirectory { return &AgentDirectory{db: db} }

// Register claims (or refreshes) an endpoint for this instance.
func (d *AgentDirectory) Register(ctx context.Context, endpoint, instanceID, internalURL string) error {
	_, err := d.db.Exec(ctx, `
		INSERT INTO agent_sessions(endpoint, instance_id, internal_url) VALUES($1, $2, $3)
		ON CONFLICT(endpoint) DO UPDATE SET instance_id=excluded.instance_id, internal_url=excluded.internal_url,
		    connected_at=CASE WHEN agent_sessions.instance_id=excluded.instance_id THEN agent_sessions.connected_at ELSE now() END,
		    heartbeat_at=now()
	`, endpoint, instanceID, internalURL)
	return err
}

// Unregister releases an endpoint only if this instance still holds it; a
// newer connection on another instance keeps its claim.
func (d *AgentDirectory) Unregister(ctx context.Context, endpoint, instanceID string) error {
	_, err := d.db.Exec(ctx, "DELETE FROM agent_sessions WHERE endpoint=$1 AND instance_id=$2", endpoint, instanceID)
	return err
}

// Lookup returns the internal URL of the other instance holding endpoint.
func (d *AgentDirectory) Lookup(ctx context.Context, endpoint, selfID string) (string, bool, error) {
	var internalURL string
	err := d.db.QueryRow(ctx, `
		SELECT internal_url FROM agent_sessions
		WHERE endpoint=$1 AND instance_id<>$2 AND heartbeat_at > now() - make_interval(secs => $3)
	`, endpoint, selfID, agentSessionTTL.Seconds()).Scan(&internalURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return internalURL, err == nil, err
}
