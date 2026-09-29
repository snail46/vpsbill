-- Install commands carry an enroll key: one per account for hosting, one for
-- the platform. An agent connecting with it, whose token no node uses yet,
-- is listed for that owner until it is added as a node or dismissed.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS agent_enroll_key text UNIQUE;
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS agent_enroll_key text;

CREATE TABLE IF NOT EXISTS agent_enrollments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint text NOT NULL UNIQUE,
    -- NULL for agents installed with the platform's key.
    account_id uuid REFERENCES accounts(id) ON DELETE CASCADE,
    token_ciphertext bytea NOT NULL,
    hostname text NOT NULL DEFAULT '',
    agent_version text NOT NULL DEFAULT '',
    runtimes text[] NOT NULL DEFAULT '{}',
    remote_ip text NOT NULL DEFAULT '',
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_enrollments_account_idx ON agent_enrollments(account_id);
