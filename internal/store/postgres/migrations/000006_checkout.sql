ALTER TABLE plans
    ADD COLUMN default_template_id text NOT NULL DEFAULT 'debian-bookworm',
    ADD COLUMN allowed_template_ids text[] NOT NULL DEFAULT ARRAY['debian-bookworm']::text[];

CREATE TABLE payment_intents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id),
    invoice_id uuid NOT NULL REFERENCES invoices(id),
    provider text NOT NULL,
    merchant_reference text NOT NULL UNIQUE,
    external_reference text,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'redirected', 'succeeded', 'failed', 'expired', 'cancelled')),
    currency char(3) NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    checkout_url text,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX payment_intents_active_invoice_idx
    ON payment_intents(invoice_id, provider)
    WHERE status IN ('pending', 'redirected');
CREATE INDEX payment_intents_account_created_idx
    ON payment_intents(account_id, created_at DESC);
