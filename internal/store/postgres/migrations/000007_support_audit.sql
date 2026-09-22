CREATE TABLE support_tickets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number text NOT NULL UNIQUE,
    account_id uuid NOT NULL REFERENCES accounts(id),
    requester_user_id uuid NOT NULL REFERENCES users(id),
    assigned_staff_id uuid REFERENCES users(id),
    service_id uuid REFERENCES services(id),
    subject text NOT NULL CHECK (char_length(subject) BETWEEN 3 AND 160),
    priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'customer_reply', 'staff_reply', 'resolved', 'closed')),
    last_reply_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE support_messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_type text NOT NULL CHECK (author_type IN ('customer', 'staff', 'system')),
    author_id uuid REFERENCES users(id),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    internal boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX support_tickets_account_updated_idx ON support_tickets(account_id, updated_at DESC);
CREATE INDEX support_tickets_status_updated_idx ON support_tickets(status, updated_at DESC);
CREATE INDEX support_messages_ticket_created_idx ON support_messages(ticket_id, created_at);
CREATE INDEX audit_created_idx ON audit_logs(created_at DESC);
