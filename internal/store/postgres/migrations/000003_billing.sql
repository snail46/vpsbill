ALTER TABLE invoice_lines ADD COLUMN order_item_id uuid REFERENCES order_items(id);
ALTER TABLE services ADD COLUMN billing_cycle text CHECK (billing_cycle IN ('monthly', 'quarterly', 'semiannual', 'annual'));

ALTER TABLE outbox_events ADD COLUMN deduplication_key text;
UPDATE outbox_events SET deduplication_key=id::text WHERE deduplication_key IS NULL;
ALTER TABLE outbox_events ALTER COLUMN deduplication_key SET NOT NULL;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_deduplication_key_unique UNIQUE (deduplication_key);

CREATE INDEX payment_events_received_idx ON payment_events(received_at DESC);
CREATE INDEX transactions_invoice_idx ON transactions(invoice_id, created_at DESC);
CREATE INDEX orders_account_created_idx ON orders(account_id, created_at DESC);

CREATE OR REPLACE FUNCTION reject_financial_record_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'financial ledger records are immutable; insert a reversing entry instead';
END;
$$;

CREATE TRIGGER transactions_are_immutable
BEFORE UPDATE OR DELETE ON transactions
FOR EACH ROW EXECUTE FUNCTION reject_financial_record_mutation();

CREATE TRIGGER processed_payment_events_are_immutable
BEFORE UPDATE OR DELETE ON payment_events
FOR EACH ROW
WHEN (OLD.processed_at IS NOT NULL)
EXECUTE FUNCTION reject_financial_record_mutation();

