-- Trading market: customers resell running instances to each other for
-- balance. Only instances held for at least 31 days can be listed.
CREATE TABLE IF NOT EXISTS service_listings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id uuid NOT NULL REFERENCES services(id),
    seller_account_id uuid NOT NULL REFERENCES accounts(id),
    buyer_account_id uuid REFERENCES accounts(id),
    currency char(3) NOT NULL,
    price_minor bigint NOT NULL CHECK (price_minor BETWEEN 100 AND 10000000),
    note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    status text NOT NULL DEFAULT 'listed' CHECK (status IN ('listed', 'sold', 'cancelled')),
    cancel_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    sold_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS service_listings_one_open ON service_listings(service_id) WHERE status = 'listed';
CREATE INDEX IF NOT EXISTS service_listings_status_idx ON service_listings(status, created_at DESC);

-- When the current owner got the instance: the purchase, or the last trade.
ALTER TABLE services ADD COLUMN IF NOT EXISTS acquired_at timestamptz;
UPDATE services SET acquired_at = created_at WHERE acquired_at IS NULL;
ALTER TABLE services ALTER COLUMN acquired_at SET DEFAULT now();

ALTER TABLE wallet_entries DROP CONSTRAINT IF EXISTS wallet_entries_kind_check;
ALTER TABLE wallet_entries ADD CONSTRAINT wallet_entries_kind_check
    CHECK (kind IN ('topup', 'earning', 'payment', 'clearance_refund', 'clearance_penalty', 'adjustment', 'refund', 'trade_purchase', 'trade_sale'));
