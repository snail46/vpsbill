-- The customer portal's "联系我们" page: a short intro and the ways to reach
-- the platform (Telegram, QQ group, mail, ...), set in 站点设置.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS contact_intro text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS contact_links jsonb NOT NULL DEFAULT '[]';
