-- Every instance renews from the account balance by default; customers can
-- switch it off per instance.
ALTER TABLE services
    ADD COLUMN auto_renew boolean NOT NULL DEFAULT true,
    -- template_id is the system currently installed, set on reinstall;
    -- until then the ordered template applies.
    ADD COLUMN template_id text;

-- Announcements staff publish on the customer overview.
CREATE TABLE announcements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 4000),
    pinned boolean NOT NULL DEFAULT false,
    published boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX announcements_listing_idx ON announcements (published, pinned DESC, created_at DESC);
