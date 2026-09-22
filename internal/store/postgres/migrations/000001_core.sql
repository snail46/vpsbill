CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (kind IN ('individual', 'business')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('pending', 'active', 'suspended', 'closed')),
    display_name text NOT NULL,
    billing_email text NOT NULL,
    legal_name text,
    tax_id text,
    country_code char(2),
    default_currency char(3) NOT NULL DEFAULT 'CNY',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    password_hash text NOT NULL,
    email_verified_at timestamptz,
    totp_enabled boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('pending', 'active', 'locked', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_unique_idx ON users (lower(email));

CREATE TABLE memberships (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'billing', 'operator', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, user_id)
);

CREATE TABLE staff_roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    permissions text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE staff_members (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES staff_roles(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE regions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE nodes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    region_id uuid NOT NULL REFERENCES regions(id),
    name text NOT NULL UNIQUE,
    base_url text NOT NULL,
    api_key_ciphertext bytea NOT NULL,
    status text NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'online', 'degraded', 'offline', 'maintenance')),
    virtualization_types text[] NOT NULL DEFAULT '{}',
    capacity jsonb NOT NULL DEFAULT '{}',
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    virtualization text NOT NULL CHECK (virtualization IN ('lxc', 'kvm')),
    vcpu integer NOT NULL CHECK (vcpu > 0),
    ram_mb integer NOT NULL CHECK (ram_mb > 0),
    disk_gb integer NOT NULL CHECK (disk_gb > 0),
    traffic_gb integer NOT NULL DEFAULT 0 CHECK (traffic_gb >= 0),
    network_down_mbps integer NOT NULL DEFAULT 0 CHECK (network_down_mbps >= 0),
    network_up_mbps integer NOT NULL DEFAULT 0 CHECK (network_up_mbps >= 0),
    snapshot_limit integer NOT NULL DEFAULT 0 CHECK (snapshot_limit >= 0),
    enabled boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plan_prices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id uuid NOT NULL REFERENCES plans(id),
    currency char(3) NOT NULL,
    billing_cycle text NOT NULL CHECK (billing_cycle IN ('monthly', 'quarterly', 'semiannual', 'annual')),
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    setup_fee_minor bigint NOT NULL DEFAULT 0 CHECK (setup_fee_minor >= 0),
    active_from timestamptz NOT NULL DEFAULT now(),
    active_until timestamptz,
    UNIQUE (plan_id, currency, billing_cycle, active_from)
);

CREATE TABLE orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number text NOT NULL UNIQUE,
    account_id uuid NOT NULL REFERENCES accounts(id),
    status text NOT NULL CHECK (status IN ('draft', 'pending_payment', 'review', 'paid', 'fulfilling', 'completed', 'cancelled', 'fraud')),
    currency char(3) NOT NULL,
    subtotal_minor bigint NOT NULL CHECK (subtotal_minor >= 0),
    tax_minor bigint NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
    total_minor bigint NOT NULL CHECK (total_minor >= 0),
    risk_score integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE order_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    plan_id uuid NOT NULL REFERENCES plans(id),
    region_id uuid NOT NULL REFERENCES regions(id),
    description text NOT NULL,
    quantity integer NOT NULL DEFAULT 1 CHECK (quantity > 0),
    unit_amount_minor bigint NOT NULL CHECK (unit_amount_minor >= 0),
    configuration jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invoices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number text NOT NULL UNIQUE,
    account_id uuid NOT NULL REFERENCES accounts(id),
    order_id uuid REFERENCES orders(id),
    status text NOT NULL CHECK (status IN ('draft', 'open', 'paid', 'void', 'uncollectible', 'refunded')),
    currency char(3) NOT NULL,
    subtotal_minor bigint NOT NULL CHECK (subtotal_minor >= 0),
    tax_minor bigint NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
    total_minor bigint NOT NULL CHECK (total_minor >= 0),
    balance_minor bigint NOT NULL CHECK (balance_minor >= 0),
    issued_at timestamptz,
    due_at timestamptz NOT NULL,
    paid_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invoice_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id uuid NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    description text NOT NULL,
    quantity integer NOT NULL CHECK (quantity > 0),
    unit_amount_minor bigint NOT NULL,
    tax_minor bigint NOT NULL DEFAULT 0,
    total_minor bigint NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE payment_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL,
    provider_event_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    processing_error text,
    UNIQUE (provider, provider_event_id)
);

CREATE TABLE transactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id),
    invoice_id uuid REFERENCES invoices(id),
    provider text NOT NULL,
    provider_transaction_id text,
    type text NOT NULL CHECK (type IN ('payment', 'refund', 'credit', 'chargeback', 'adjustment')),
    status text NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'reversed')),
    currency char(3) NOT NULL,
    amount_minor bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_transaction_id)
);

CREATE TABLE services (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id),
    order_item_id uuid UNIQUE REFERENCES order_items(id),
    plan_id uuid NOT NULL REFERENCES plans(id),
    region_id uuid NOT NULL REFERENCES regions(id),
    node_id uuid REFERENCES nodes(id),
    status text NOT NULL CHECK (status IN ('pending_payment', 'provisioning', 'active', 'overdue', 'suspended', 'terminating', 'terminated', 'error', 'review')),
    instance_name text NOT NULL UNIQUE,
    external_id text,
    external_uuid text,
    hostname text,
    primary_ipv4 inet,
    primary_ipv6 inet,
    next_due_at timestamptz,
    expires_at timestamptz,
    suspended_at timestamptz,
    terminated_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE provisioning_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id uuid NOT NULL REFERENCES services(id),
    action text NOT NULL CHECK (action IN ('provision', 'start', 'stop', 'restart', 'reinstall', 'resize', 'suspend', 'unsuspend', 'terminate', 'reconcile')),
    deduplication_key text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'dead')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_at timestamptz,
    locked_by text,
    external_task_id text,
    last_error text,
    payload jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    published_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id bigserial PRIMARY KEY,
    actor_type text NOT NULL,
    actor_id text,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text,
    ip inet,
    user_agent text,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX services_account_status_idx ON services(account_id, status);
CREATE INDEX services_node_status_idx ON services(node_id, status);
CREATE INDEX invoices_account_status_due_idx ON invoices(account_id, status, due_at);
CREATE INDEX jobs_ready_idx ON provisioning_jobs(status, available_at) WHERE status IN ('pending', 'failed');
CREATE INDEX outbox_unpublished_idx ON outbox_events(created_at) WHERE published_at IS NULL;
CREATE INDEX audit_target_idx ON audit_logs(target_type, target_id, created_at DESC);
