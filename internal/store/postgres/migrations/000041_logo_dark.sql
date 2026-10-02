-- A second logo for the dark theme, uploaded or linked like the first one.
-- When it is not set, the dark theme shows the light logo.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_dark_url text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_dark_image bytea;
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_dark_content_type text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_dark_version text NOT NULL DEFAULT '';
