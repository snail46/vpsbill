-- Telegram, first stage of the roadmap.

-- Buttons under a queued Telegram message: rows of {text, url | data}.
ALTER TABLE mail_queue ADD COLUMN IF NOT EXISTS buttons jsonb;

-- The rebate an inviter gets for an invited member's first real payment;
-- one per invited site account.
CREATE TABLE IF NOT EXISTS telegram_rebates (
    invitee_account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    inviter_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    transaction_id uuid,
    paid_minor bigint NOT NULL,
    amount_minor bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telegram_rebates_inviter_idx ON telegram_rebates(inviter_account_id);

-- Customers waiting for a sold-out plan; they are told once when it can
-- be bought again.
CREATE TABLE IF NOT EXISTS plan_watches (
    plan_id uuid NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, user_id)
);
CREATE INDEX IF NOT EXISTS plan_watches_user_idx ON plan_watches(user_id);

-- Whether each plan could be bought at the last look, to notice new plans
-- and restocks.
CREATE TABLE IF NOT EXISTS plan_stock_states (
    plan_id uuid PRIMARY KEY REFERENCES plans(id) ON DELETE CASCADE,
    available boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
