-- How the logo sits in the top-left corner: auto decides by its shape,
-- icon keeps the site name beside it, wordmark (a logo that already
-- carries the brand name) takes the name's place.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_mode text NOT NULL DEFAULT 'auto';
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_logo_mode_check;
ALTER TABLE system_settings ADD CONSTRAINT system_settings_logo_mode_check CHECK (logo_mode IN ('auto', 'icon', 'wordmark'));

-- How long an owner must hold an instance before listing it in the
-- trading market.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS trade_hold_days integer NOT NULL DEFAULT 31;
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_trade_hold_days_check;
ALTER TABLE system_settings ADD CONSTRAINT system_settings_trade_hold_days_check CHECK (trade_hold_days BETWEEN 0 AND 365);

-- The latest reinstall of each service: it runs in the background, and the
-- customer's page follows it here.
CREATE TABLE IF NOT EXISTS service_reinstalls (
    service_id uuid PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
    template_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
