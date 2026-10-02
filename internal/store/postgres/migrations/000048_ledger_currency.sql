-- The ledger currency can be switched between CNY and USD (it is kept in
-- system_settings.locale_settings->>'ledger_currency'). Each switch
-- converts every amount at the rate of the moment and is recorded here.
CREATE TABLE IF NOT EXISTS ledger_switches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_currency char(3) NOT NULL,
    to_currency char(3) NOT NULL,
    -- CNY for one USD.
    usd_rate numeric(12,4) NOT NULL,
    actor_user_id uuid,
    counts jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- What the gateway charges for an intent when it differs from the invoice:
-- Alipay and Epay take CNY, whatever the ledger currency is.
ALTER TABLE payment_intents
    ADD COLUMN IF NOT EXISTS charge_currency char(3),
    ADD COLUMN IF NOT EXISTS charge_minor bigint CHECK (charge_minor > 0);

-- Transactions and wallet entries stay immutable, with one exception: a
-- switch of the ledger currency re-denominates them together with
-- everything else. ConvertLedger turns the setting on for its transaction
-- only; rows still cannot be deleted.
CREATE OR REPLACE FUNCTION reject_financial_record_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND current_setting('vpsbill.ledger_conversion', true) = 'on' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'financial ledger records are immutable; insert a reversing entry instead';
END;
$$;
