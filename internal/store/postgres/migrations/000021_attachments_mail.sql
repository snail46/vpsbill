-- Image attachments on support ticket messages, stored in the database so
-- they are covered by the regular backups.
CREATE TABLE IF NOT EXISTS support_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES support_messages(id) ON DELETE CASCADE,
    file_name text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 200),
    content_type text NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/gif', 'image/webp')),
    size_bytes integer NOT NULL CHECK (size_bytes > 0),
    data bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS support_attachments_message_idx ON support_attachments(message_id);

-- Outgoing mail. dedup_key keeps reminders from repeating.
CREATE TABLE IF NOT EXISTS mail_queue (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    recipient text NOT NULL,
    subject text NOT NULL,
    body text NOT NULL,
    dedup_key text UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);
CREATE INDEX IF NOT EXISTS mail_queue_due_idx ON mail_queue(status, next_attempt_at);

-- Host rental expiry and monthly transfer allowance, for merchant reminders.
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS expires_at date,
    ADD COLUMN IF NOT EXISTS traffic_quota_gb integer NOT NULL DEFAULT 0 CHECK (traffic_quota_gb >= 0);

-- Last measured traffic for the current calendar month.
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS traffic_used_bytes bigint,
    ADD COLUMN IF NOT EXISTS traffic_measured_at timestamptz;

ALTER TABLE system_settings
    ADD COLUMN IF NOT EXISTS mail_notifications jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS ticket_attachment_max_mb integer NOT NULL DEFAULT 5
        CHECK (ticket_attachment_max_mb BETWEEN 1 AND 20);
