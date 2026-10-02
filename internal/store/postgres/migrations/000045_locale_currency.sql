-- Display language and currency: the site's defaults and the USD exchange
-- rate (the ledger stays in CNY), and each user's language for mail.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS locale_settings jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locale text NOT NULL DEFAULT '';
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_locale_check;
ALTER TABLE users ADD CONSTRAINT users_locale_check CHECK (locale IN ('', 'zh', 'en'));
