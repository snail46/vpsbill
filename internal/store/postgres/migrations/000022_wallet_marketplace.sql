-- Account balance. Top-ups and hosting earnings land here; the balance can
-- only be spent on this platform, never withdrawn.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS balance_minor bigint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS wallet_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id),
    kind text NOT NULL CHECK (kind IN ('topup', 'earning', 'payment', 'clearance_refund', 'clearance_penalty', 'adjustment')),
    amount_minor bigint NOT NULL CHECK (amount_minor <> 0),
    balance_after_minor bigint NOT NULL,
    currency char(3) NOT NULL,
    description text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 300),
    reference_type text,
    reference_id uuid,
    deduplication_key text UNIQUE,
    actor_user_id uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wallet_entries_account_idx ON wallet_entries(account_id, created_at DESC);
DROP TRIGGER IF EXISTS wallet_entries_are_immutable ON wallet_entries;
CREATE TRIGGER wallet_entries_are_immutable
BEFORE UPDATE OR DELETE ON wallet_entries
FOR EACH ROW EXECUTE FUNCTION reject_financial_record_mutation();

-- Top-up invoices credit the balance when paid.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_kind_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('initial', 'renewal', 'topup'));

-- Hosted nodes belong to a customer account and are listed in the hosting
-- market. Platform nodes keep owner_account_id NULL.
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS owner_account_id uuid REFERENCES accounts(id),
    ADD COLUMN IF NOT EXISTS location text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS line_description text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS listing_status text NOT NULL DEFAULT 'listed',
    ADD COLUMN IF NOT EXISTS clearance_hold_until timestamptz,
    ADD COLUMN IF NOT EXISTS offline_notified_at timestamptz,
    ADD COLUMN IF NOT EXISTS retired_at timestamptz,
    ADD COLUMN IF NOT EXISTS retired_reason text;
ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_listing_status_check;
ALTER TABLE nodes ADD CONSTRAINT nodes_listing_status_check CHECK (listing_status IN ('listed', 'paused', 'retired'));
CREATE INDEX IF NOT EXISTS nodes_owner_idx ON nodes(owner_account_id) WHERE owner_account_id IS NOT NULL;

-- Hosted plans are pinned to their owner's node.
ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS owner_account_id uuid REFERENCES accounts(id),
    ADD COLUMN IF NOT EXISTS node_id uuid REFERENCES nodes(id);
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_hosted_node_check;
ALTER TABLE plans ADD CONSTRAINT plans_hosted_node_check CHECK ((owner_account_id IS NULL) = (node_id IS NULL));

ALTER TABLE services ADD COLUMN IF NOT EXISTS termination_reason text;

-- Money a buyer paid for a hosted service. The host share is released to the
-- host's balance day by day over the paid period; what is not released yet is
-- the service's remaining value, refunded when the host is cleared out.
CREATE TABLE IF NOT EXISTS marketplace_escrows (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id uuid NOT NULL REFERENCES services(id),
    invoice_id uuid NOT NULL REFERENCES invoices(id),
    node_id uuid NOT NULL REFERENCES nodes(id),
    host_account_id uuid NOT NULL REFERENCES accounts(id),
    buyer_account_id uuid NOT NULL REFERENCES accounts(id),
    currency char(3) NOT NULL,
    gross_minor bigint NOT NULL CHECK (gross_minor >= 0),
    fee_percent numeric(5,2) NOT NULL CHECK (fee_percent >= 0 AND fee_percent <= 100),
    fee_minor bigint NOT NULL CHECK (fee_minor >= 0),
    host_share_minor bigint NOT NULL CHECK (host_share_minor >= 0),
    released_days integer NOT NULL DEFAULT 0,
    released_gross_minor bigint NOT NULL DEFAULT 0,
    released_host_minor bigint NOT NULL DEFAULT 0,
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL CHECK (period_end > period_start),
    status text NOT NULL DEFAULT 'holding' CHECK (status IN ('holding', 'released', 'cleared')),
    refunded_minor bigint NOT NULL DEFAULT 0,
    cleared_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (invoice_id, service_id)
);
CREATE INDEX IF NOT EXISTS marketplace_escrows_holding_idx ON marketplace_escrows(status, period_start) WHERE status = 'holding';
CREATE INDEX IF NOT EXISTS marketplace_escrows_node_idx ON marketplace_escrows(node_id);
CREATE INDEX IF NOT EXISTS marketplace_escrows_host_idx ON marketplace_escrows(host_account_id);

-- Tickets about a hosted service go to the host first; staff can still see
-- and answer them.
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS host_account_id uuid REFERENCES accounts(id);
CREATE INDEX IF NOT EXISTS support_tickets_host_idx ON support_tickets(host_account_id, updated_at DESC) WHERE host_account_id IS NOT NULL;
ALTER TABLE support_messages DROP CONSTRAINT IF EXISTS support_messages_author_type_check;
ALTER TABLE support_messages ADD CONSTRAINT support_messages_author_type_check CHECK (author_type IN ('customer', 'staff', 'system', 'host'));

-- One chat room per hosted node: the host, its buyers and staff.
CREATE TABLE IF NOT EXISTS node_chat_messages (
    id bigserial PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES nodes(id),
    author_type text NOT NULL CHECK (author_type IN ('host', 'buyer', 'staff', 'system')),
    author_user_id uuid REFERENCES users(id),
    author_account_id uuid REFERENCES accounts(id),
    author_name text NOT NULL,
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS node_chat_messages_node_idx ON node_chat_messages(node_id, id DESC);

ALTER TABLE system_settings
    ADD COLUMN IF NOT EXISTS marketplace_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS marketplace_fee_percent numeric(5,2) NOT NULL DEFAULT 20,
    ADD COLUMN IF NOT EXISTS marketplace_offline_hours integer NOT NULL DEFAULT 24;
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_marketplace_check;
ALTER TABLE system_settings ADD CONSTRAINT system_settings_marketplace_check
    CHECK (marketplace_fee_percent >= 0 AND marketplace_fee_percent <= 90 AND marketplace_offline_hours BETWEEN 1 AND 720);
