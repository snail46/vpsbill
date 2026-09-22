ALTER TABLE users
    ADD COLUMN mfa_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN mfa_secret_encrypted bytea,
    ADD COLUMN mfa_pending_secret_encrypted bytea;

CREATE TABLE login_attempts (
    id bigserial PRIMARY KEY,
    email_hash bytea NOT NULL,
    ip inet,
    successful boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX login_attempts_email_failed_idx ON login_attempts(email_hash, created_at DESC) WHERE successful=false;
CREATE INDEX login_attempts_ip_failed_idx ON login_attempts(ip, created_at DESC) WHERE successful=false;
CREATE INDEX login_attempts_created_idx ON login_attempts(created_at);
