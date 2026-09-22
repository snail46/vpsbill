ALTER TABLE users ADD COLUMN display_name text NOT NULL DEFAULT '';

CREATE TABLE login_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip inet,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX login_sessions_user_idx ON login_sessions(user_id, expires_at DESC);
CREATE INDEX login_sessions_expiry_idx ON login_sessions(expires_at);

INSERT INTO staff_roles (name, permissions)
VALUES ('Super Administrator', ARRAY['*']::text[])
ON CONFLICT (name) DO NOTHING;

