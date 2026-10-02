-- Notification channels: a user with a linked Telegram account chooses
-- mail, Telegram or both; the queue carries messages for either.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS notify_email boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS notify_telegram boolean NOT NULL DEFAULT false;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_notify_check;
ALTER TABLE users ADD CONSTRAINT users_notify_check CHECK (notify_email OR notify_telegram);

ALTER TABLE mail_queue ADD COLUMN IF NOT EXISTS channel text NOT NULL DEFAULT 'mail';
ALTER TABLE mail_queue DROP CONSTRAINT IF EXISTS mail_queue_channel_check;
ALTER TABLE mail_queue ADD CONSTRAINT mail_queue_channel_check CHECK (channel IN ('mail', 'telegram'));
DROP INDEX IF EXISTS mail_queue_due_idx;
CREATE INDEX IF NOT EXISTS mail_queue_due_idx ON mail_queue(channel, status, next_attempt_at);

-- Staff roles by level. Permissions are "<area>:read" and "<area>:write";
-- the roles are fixed, staff are given one of them.
ALTER TABLE staff_roles ADD COLUMN IF NOT EXISTS level integer NOT NULL DEFAULT 100;
UPDATE staff_roles SET level=1 WHERE name='Super Administrator';

INSERT INTO staff_roles(name, level, permissions) VALUES
    ('Administrator', 2, ARRAY[
        'customers:read','customers:write','orders:read','orders:write','billing:read','billing:write',
        'services:read','services:write','nodes:read','nodes:write','plans:read','plans:write',
        'tickets:read','tickets:write','settings:read','settings:write','audit:read']::text[]),
    ('Operations', 3, ARRAY[
        'customers:read','orders:read','orders:write','billing:read',
        'services:read','services:write','nodes:read','nodes:write','plans:read','plans:write',
        'tickets:read','tickets:write','settings:read']::text[]),
    ('Finance', 4, ARRAY[
        'customers:read','orders:read','orders:write','billing:read','billing:write',
        'services:read','nodes:read','plans:read','tickets:read']::text[]),
    ('Support', 5, ARRAY[
        'customers:read','orders:read','billing:read','services:read','services:write',
        'nodes:read','plans:read','tickets:read','tickets:write']::text[]),
    ('Read Only', 6, ARRAY[
        'customers:read','orders:read','billing:read','services:read','nodes:read','plans:read',
        'tickets:read','settings:read','audit:read']::text[])
ON CONFLICT (name) DO UPDATE SET level=EXCLUDED.level, permissions=EXCLUDED.permissions;
