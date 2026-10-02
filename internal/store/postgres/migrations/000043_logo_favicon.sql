-- The browser tab icon, set apart from the logo (ICO, PNG or SVG). When it
-- is not set, the tab shows the logo.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_favicon_url text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_favicon_image bytea;
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_favicon_content_type text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_favicon_version text NOT NULL DEFAULT '';
