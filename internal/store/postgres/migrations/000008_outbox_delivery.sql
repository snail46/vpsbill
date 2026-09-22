ALTER TABLE outbox_events
    ADD COLUMN available_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN locked_at timestamptz,
    ADD COLUMN locked_by text,
    ADD COLUMN last_error text;

CREATE INDEX outbox_delivery_ready_idx
    ON outbox_events(available_at, created_at)
    WHERE published_at IS NULL;
