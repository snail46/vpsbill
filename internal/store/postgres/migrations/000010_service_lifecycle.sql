ALTER TABLE invoices
    ADD COLUMN service_id uuid REFERENCES services(id),
    ADD COLUMN kind text NOT NULL DEFAULT 'initial' CHECK (kind IN ('initial', 'renewal')),
    ADD COLUMN period_start timestamptz,
    ADD COLUMN period_end timestamptz;

ALTER TABLE services
    ADD COLUMN grace_until timestamptz,
    ADD COLUMN termination_scheduled_at timestamptz;

CREATE UNIQUE INDEX invoices_service_period_unique
    ON invoices(service_id, period_end)
    WHERE service_id IS NOT NULL;
CREATE INDEX services_lifecycle_due_idx
    ON services(status, next_due_at)
    WHERE status IN ('active', 'overdue', 'suspended');
