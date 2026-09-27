-- One-time password reset links, requested by email or issued by staff.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_by text NOT NULL CHECK (created_by IN ('self', 'staff')),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS password_reset_tokens_user_idx ON password_reset_tokens(user_id, created_at);

-- Outgoing mail for password reset links.
ALTER TABLE system_settings
    ADD COLUMN IF NOT EXISTS smtp_host text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS smtp_port integer NOT NULL DEFAULT 587,
    ADD COLUMN IF NOT EXISTS smtp_username text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS smtp_password_encrypted bytea,
    ADD COLUMN IF NOT EXISTS smtp_from text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS smtp_security text NOT NULL DEFAULT 'starttls'
        CHECK (smtp_security IN ('starttls', 'tls', 'none'));
