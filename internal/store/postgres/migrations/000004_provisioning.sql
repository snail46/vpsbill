ALTER TABLE nodes
    ADD COLUMN capacity_vcpu integer NOT NULL DEFAULT 0 CHECK (capacity_vcpu >= 0),
    ADD COLUMN capacity_ram_mb bigint NOT NULL DEFAULT 0 CHECK (capacity_ram_mb >= 0),
    ADD COLUMN capacity_disk_gb bigint NOT NULL DEFAULT 0 CHECK (capacity_disk_gb >= 0);

ALTER TABLE services
    ADD COLUMN runtime_status text NOT NULL DEFAULT 'unknown'
        CHECK (runtime_status IN ('unknown', 'creating', 'running', 'stopped', 'suspended', 'missing', 'error')),
    ADD COLUMN last_reconciled_at timestamptz,
    ADD COLUMN last_reconcile_error text;

-- One order line can legitimately request multiple identical VPS instances.
ALTER TABLE services DROP CONSTRAINT IF EXISTS services_order_item_id_key;
CREATE INDEX services_order_item_idx ON services(order_item_id);

CREATE TABLE inventory_reservations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id uuid NOT NULL UNIQUE REFERENCES services(id),
    node_id uuid NOT NULL REFERENCES nodes(id),
    vcpu integer NOT NULL CHECK (vcpu > 0),
    ram_mb integer NOT NULL CHECK (ram_mb > 0),
    disk_gb integer NOT NULL CHECK (disk_gb > 0),
    status text NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved', 'released')),
    created_at timestamptz NOT NULL DEFAULT now(),
    released_at timestamptz
);

CREATE INDEX inventory_reservations_node_idx
    ON inventory_reservations(node_id) WHERE status='reserved';
CREATE INDEX jobs_running_lock_idx
    ON provisioning_jobs(locked_at) WHERE status='running';
CREATE INDEX services_reconcile_idx
    ON services(last_reconciled_at) WHERE status IN ('active', 'suspended', 'overdue');
