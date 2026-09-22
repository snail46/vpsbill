ALTER TABLE services
    ADD COLUMN desired_runtime_status text
        CHECK (desired_runtime_status IN ('running', 'stopped'));

-- Only one unfinished operation of the same kind may exist per service. Once
-- it succeeds or is cancelled by manual handling, a later operation is valid.
CREATE UNIQUE INDEX provisioning_jobs_active_action_idx
    ON provisioning_jobs(service_id, action)
    WHERE status IN ('pending', 'running', 'failed');

CREATE INDEX memberships_user_idx ON memberships(user_id, created_at);
CREATE INDEX accounts_billing_email_idx ON accounts(lower(billing_email));
