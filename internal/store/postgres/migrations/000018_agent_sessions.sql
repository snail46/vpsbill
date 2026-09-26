-- Which API instance holds each Hatch agent connection, so other instances
-- can forward requests to it. Rows without a recent heartbeat are ignored.
CREATE TABLE agent_sessions (
    endpoint text PRIMARY KEY,
    instance_id text NOT NULL,
    internal_url text NOT NULL,
    connected_at timestamptz NOT NULL DEFAULT now(),
    heartbeat_at timestamptz NOT NULL DEFAULT now()
);
