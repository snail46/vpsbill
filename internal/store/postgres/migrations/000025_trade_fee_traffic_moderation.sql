-- Trading market fee, and what a listing showed buyers when it went up.
ALTER TABLE system_settings
    ADD COLUMN IF NOT EXISTS trade_fee_percent numeric(5,2) NOT NULL DEFAULT 20 CHECK (trade_fee_percent BETWEEN 0 AND 90),
    ADD COLUMN IF NOT EXISTS admin_url text NOT NULL DEFAULT '';
ALTER TABLE service_listings
    ADD COLUMN IF NOT EXISTS fee_percent numeric(5,2),
    ADD COLUMN IF NOT EXISTS fee_minor bigint,
    ADD COLUMN IF NOT EXISTS seller_proceeds_minor bigint,
    ADD COLUMN IF NOT EXISTS traffic_total_bytes bigint,
    ADD COLUMN IF NOT EXISTS traffic_rx_bytes bigint,
    ADD COLUMN IF NOT EXISTS traffic_tx_bytes bigint,
    ADD COLUMN IF NOT EXISTS traffic_measured_at timestamptz;

-- A listed instance is stopped and frozen until it sells or is withdrawn.
-- Listings close by themselves when the instance stops being active.
CREATE OR REPLACE FUNCTION close_listings_of_inactive_service() RETURNS trigger AS $$
BEGIN
    UPDATE service_listings SET status='cancelled', cancel_reason='实例已不是正常运行状态', updated_at=now()
    WHERE service_id=NEW.id AND status='listed';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS services_close_listings ON services;
CREATE TRIGGER services_close_listings
AFTER UPDATE OF status ON services
FOR EACH ROW WHEN (NEW.status <> 'active' AND OLD.status = 'active')
EXECUTE FUNCTION close_listings_of_inactive_service();

-- Monthly traffic counts both directions; keep the split for display, and
-- the month in which an instance was stopped for exceeding its allowance.
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS traffic_rx_bytes bigint,
    ADD COLUMN IF NOT EXISTS traffic_tx_bytes bigint,
    ADD COLUMN IF NOT EXISTS traffic_locked_month text,
    -- Hosted instances renew at the price they were sold at, or lower.
    ADD COLUMN IF NOT EXISTS renewal_price_minor bigint;

-- Email verification for new customer accounts. Existing users are treated
-- as verified.
UPDATE users SET email_verified_at = coalesce(email_verified_at, created_at) WHERE email_verified_at IS NULL;
CREATE TABLE IF NOT EXISTS email_verifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS email_verifications_user_idx ON email_verifications(user_id, created_at DESC);

-- A TOTP code works once: the last accepted time step is remembered.
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step bigint;

-- Chat moderation: staff can mute an account in a room.
CREATE TABLE IF NOT EXISTS node_chat_mutes (
    node_id uuid NOT NULL REFERENCES nodes(id),
    account_id uuid NOT NULL REFERENCES accounts(id),
    until timestamptz NOT NULL,
    reason text NOT NULL DEFAULT '',
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, account_id)
);

-- Customer reports about hosted nodes (false resources, overselling) and
-- chat messages.
CREATE TABLE IF NOT EXISTS reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_account_id uuid NOT NULL REFERENCES accounts(id),
    reporter_user_id uuid REFERENCES users(id),
    target_type text NOT NULL CHECK (target_type IN ('node', 'chat_message')),
    node_id uuid NOT NULL REFERENCES nodes(id),
    message_id bigint REFERENCES node_chat_messages(id),
    reason text NOT NULL CHECK (reason IN ('resources', 'oversell', 'false_info', 'abuse', 'spam', 'other')),
    detail text NOT NULL DEFAULT '' CHECK (char_length(detail) <= 1000),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolution text,
    resolved_by uuid REFERENCES users(id),
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS reports_status_idx ON reports(status, created_at DESC);

-- Staff can cap what a hosted node may sell, whatever its agent reports.
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS capacity_cap_vcpu integer CHECK (capacity_cap_vcpu > 0),
    ADD COLUMN IF NOT EXISTS capacity_cap_ram_mb bigint CHECK (capacity_cap_ram_mb > 0),
    ADD COLUMN IF NOT EXISTS capacity_cap_disk_gb bigint CHECK (capacity_cap_disk_gb > 0);
