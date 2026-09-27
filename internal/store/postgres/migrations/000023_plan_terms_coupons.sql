-- Hosted plan terms: a description for buyers, a per-buyer purchase limit
-- (0 = unlimited) and the early full refund option.
ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS purchase_limit integer NOT NULL DEFAULT 0 CHECK (purchase_limit BETWEEN 0 AND 100),
    ADD COLUMN IF NOT EXISTS early_refund boolean NOT NULL DEFAULT false;

-- Buyers can cancel a hosted service for a refund to their balance.
ALTER TABLE wallet_entries DROP CONSTRAINT IF EXISTS wallet_entries_kind_check;
ALTER TABLE wallet_entries ADD CONSTRAINT wallet_entries_kind_check
    CHECK (kind IN ('topup', 'earning', 'payment', 'clearance_refund', 'clearance_penalty', 'adjustment', 'refund'));
ALTER TABLE marketplace_escrows DROP CONSTRAINT IF EXISTS marketplace_escrows_status_check;
ALTER TABLE marketplace_escrows ADD CONSTRAINT marketplace_escrows_status_check
    CHECK (status IN ('holding', 'released', 'cleared', 'refunded'));

-- Coupons. Platform coupons (owner_account_id NULL) are created by staff and
-- apply to platform plans; host coupons apply to that host's plans only.
CREATE TABLE IF NOT EXISTS coupons (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9_-]{3,32}$'),
    owner_account_id uuid REFERENCES accounts(id),
    description text NOT NULL DEFAULT '',
    discount_type text NOT NULL CHECK (discount_type IN ('percent', 'amount')),
    discount_value bigint NOT NULL CHECK (discount_value > 0),
    -- Empty means every plan the owner sells.
    plan_ids uuid[] NOT NULL DEFAULT '{}',
    -- 0 means unlimited. Each discounted instance counts as one use.
    max_uses integer NOT NULL DEFAULT 0 CHECK (max_uses >= 0),
    expires_at timestamptz,
    -- Renewals of instances bought with the code keep the same discount.
    recurring boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (discount_type <> 'percent' OR discount_value <= 99)
);
CREATE INDEX IF NOT EXISTS coupons_owner_idx ON coupons(owner_account_id);

CREATE TABLE IF NOT EXISTS coupon_redemptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id uuid NOT NULL REFERENCES coupons(id),
    account_id uuid NOT NULL REFERENCES accounts(id),
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    invoice_id uuid NOT NULL REFERENCES invoices(id),
    units integer NOT NULL CHECK (units > 0),
    discount_minor bigint NOT NULL CHECK (discount_minor >= 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS coupon_redemptions_coupon_idx ON coupon_redemptions(coupon_id, status);

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS coupon_id uuid REFERENCES coupons(id),
    ADD COLUMN IF NOT EXISTS discount_minor bigint NOT NULL DEFAULT 0 CHECK (discount_minor >= 0);

-- The discount an order item received, and what renewals keep when the
-- coupon is recurring (copied to the service on payment).
ALTER TABLE order_items
    ADD COLUMN IF NOT EXISTS discount_unit_minor bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS renewal_discount_type text,
    ADD COLUMN IF NOT EXISTS renewal_discount_value bigint;

ALTER TABLE services
    ADD COLUMN IF NOT EXISTS coupon_id uuid REFERENCES coupons(id),
    ADD COLUMN IF NOT EXISTS renewal_discount_type text CHECK (renewal_discount_type IN ('percent', 'amount')),
    ADD COLUMN IF NOT EXISTS renewal_discount_value bigint,
    ADD COLUMN IF NOT EXISTS refunded_at timestamptz;
