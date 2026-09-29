-- Scheduled backups to the local backup directory and a WebDAV server.
-- Both tables are left out of the backups themselves (pg_dump
-- --exclude-table=backup_*), so restoring one keeps the current backup
-- settings and history.
CREATE TABLE IF NOT EXISTS backup_config (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    schedule text NOT NULL DEFAULT 'off' CHECK (schedule IN ('off', 'daily', 'interval')),
    daily_at text NOT NULL DEFAULT '03:30',
    interval_hours integer NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 168),
    keep_local integer NOT NULL DEFAULT 7 CHECK (keep_local BETWEEN 1 AND 365),
    keep_remote integer NOT NULL DEFAULT 14 CHECK (keep_remote BETWEEN 1 AND 365),
    webdav_url text NOT NULL DEFAULT '',
    webdav_username text NOT NULL DEFAULT '',
    webdav_password_encrypted bytea,
    webdav_directory text NOT NULL DEFAULT 'vpsbill',
    webdav_insecure boolean NOT NULL DEFAULT false,
    -- scheduled_at is the last schedule slot claimed, by any API instance.
    scheduled_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO backup_config(singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS backup_runs (
    id bigserial PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('backup', 'restore', 'fetch')),
    trigger text NOT NULL,
    file_name text NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL DEFAULT 0,
    -- partial: saved locally but the WebDAV upload failed.
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'partial', 'failed')),
    webdav_status text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    actor_id uuid,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS backup_runs_started_idx ON backup_runs(started_at DESC);
