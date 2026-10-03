-- Telegram, second stage of the roadmap: tickets in Telegram, red
-- packets, check-in streak bonuses, weekly leaderboards and joining the
-- group.

-- Staff link their own Telegram account to act in the staff group:
-- answer and claim tickets, hand out red packets.
CREATE TABLE IF NOT EXISTS telegram_staff_links (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    telegram_id bigint NOT NULL UNIQUE,
    username text NOT NULL DEFAULT '',
    first_name text NOT NULL DEFAULT '',
    linked_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS telegram_staff_codes (
    code_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

-- Which message the bot sent is about which ticket, so that replying to
-- it adds to that ticket. A queued notice carries its ticket until it is
-- sent.
ALTER TABLE mail_queue ADD COLUMN IF NOT EXISTS ticket_id uuid;
CREATE TABLE IF NOT EXISTS telegram_message_refs (
    chat_id bigint NOT NULL,
    message_id bigint NOT NULL,
    ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, message_id)
);

-- Red packets: a total split at random among the first count members who
-- grab it (a button) or send its password.
CREATE TABLE IF NOT EXISTS telegram_red_packets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    total_minor bigint NOT NULL CHECK (total_minor > 0),
    count integer NOT NULL CHECK (count > 0),
    remaining_minor bigint NOT NULL,
    remaining_count integer NOT NULL,
    password text,
    require_spent boolean NOT NULL DEFAULT false,
    min_linked_days integer NOT NULL DEFAULT 0,
    chat_id bigint NOT NULL DEFAULT 0,
    message_id bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','finished','expired','cancelled')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS telegram_red_packets_password_idx ON telegram_red_packets(lower(password)) WHERE status='active' AND password IS NOT NULL;
CREATE TABLE IF NOT EXISTS telegram_red_packet_claims (
    packet_id uuid NOT NULL REFERENCES telegram_red_packets(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    telegram_id bigint NOT NULL,
    amount_minor bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (packet_id, account_id),
    UNIQUE (packet_id, telegram_id)
);

-- Bonuses for checking in so many days in a row, once per streak length
-- reached on a day.
CREATE TABLE IF NOT EXISTS telegram_streak_bonuses (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    day date NOT NULL,
    days integer NOT NULL,
    amount_minor bigint NOT NULL,
    PRIMARY KEY (account_id, day, days)
);

-- Weekly leaderboards: posted once per week and board, with the prizes
-- paid.
CREATE TABLE IF NOT EXISTS telegram_leaderboards (
    week date NOT NULL,
    board text NOT NULL,
    posted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (week, board)
);
CREATE TABLE IF NOT EXISTS telegram_leaderboard_prizes (
    week date NOT NULL,
    board text NOT NULL,
    rank integer NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    count integer NOT NULL,
    amount_minor bigint NOT NULL,
    PRIMARY KEY (week, board, rank)
);

-- New members who must pass the group's check before they may write.
CREATE TABLE IF NOT EXISTS telegram_verifications (
    telegram_id bigint PRIMARY KEY,
    chat_id bigint NOT NULL,
    message_id bigint NOT NULL DEFAULT 0,
    deadline timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
