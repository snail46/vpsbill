-- Telegram: customers link their Telegram account and earn balance in the
-- site's group by checking in and inviting members.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS telegram_settings_encrypted bytea;
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS telegram_update_offset bigint NOT NULL DEFAULT 0;

-- One Telegram account per site user and the other way round.
CREATE TABLE IF NOT EXISTS telegram_links (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    telegram_id bigint NOT NULL UNIQUE,
    username text NOT NULL DEFAULT '',
    first_name text NOT NULL DEFAULT '',
    linked_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telegram_links_account_idx ON telegram_links(account_id);

-- A code opens the bot with /start bind_<code>; it works once.
CREATE TABLE IF NOT EXISTS telegram_bind_codes (
    code_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telegram_bind_codes_user_idx ON telegram_bind_codes(user_id);

-- The link bonus is paid once per Telegram account and once per site
-- account, however often they are linked again.
CREATE TABLE IF NOT EXISTS telegram_bind_rewards (
    telegram_id bigint PRIMARY KEY,
    account_id uuid NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    amount_minor bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One check-in a day (UTC+8) per site account and per Telegram account.
CREATE TABLE IF NOT EXISTS telegram_checkins (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    day date NOT NULL,
    telegram_id bigint NOT NULL,
    amount_minor bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, day),
    UNIQUE (telegram_id, day)
);

-- Everyone ever seen in the group, so leaving and joining again is not a
-- new member.
CREATE TABLE IF NOT EXISTS telegram_members (
    telegram_id bigint PRIMARY KEY,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    in_group boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Each account's own invite link to the group.
CREATE TABLE IF NOT EXISTS telegram_invite_links (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    chat_id bigint NOT NULL,
    invite_link text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A member who joined through an account's invite link. It is rewarded
-- once the member stayed long enough (and linked a site account, when the
-- settings ask for it); a Telegram account and a site account each count
-- as an invitee once.
CREATE TABLE IF NOT EXISTS telegram_invites (
    invitee_telegram_id bigint PRIMARY KEY,
    inviter_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    invitee_account_id uuid UNIQUE REFERENCES accounts(id) ON DELETE SET NULL,
    invitee_name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'rewarded', 'void')),
    void_reason text NOT NULL DEFAULT '',
    reward_minor bigint NOT NULL DEFAULT 0,
    joined_at timestamptz NOT NULL DEFAULT now(),
    left_at timestamptz,
    rewarded_at timestamptz
);
CREATE INDEX IF NOT EXISTS telegram_invites_inviter_idx ON telegram_invites(inviter_account_id, status);
CREATE INDEX IF NOT EXISTS telegram_invites_pending_idx ON telegram_invites(joined_at) WHERE status = 'pending';

ALTER TABLE wallet_entries DROP CONSTRAINT IF EXISTS wallet_entries_kind_check;
ALTER TABLE wallet_entries ADD CONSTRAINT wallet_entries_kind_check
    CHECK (kind IN ('topup', 'earning', 'payment', 'clearance_refund', 'clearance_penalty', 'adjustment', 'refund', 'trade_purchase', 'trade_sale', 'reward'));
