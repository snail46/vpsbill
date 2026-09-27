-- Overdue suspension pauses instances on nodes that support it, and the
-- worker then records "suspended" as the target runtime state.
ALTER TABLE services DROP CONSTRAINT IF EXISTS services_desired_runtime_status_check;
ALTER TABLE services ADD CONSTRAINT services_desired_runtime_status_check
    CHECK (desired_runtime_status IN ('running', 'stopped', 'suspended'));
