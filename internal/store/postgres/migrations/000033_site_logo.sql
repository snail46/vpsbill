-- The logo in the top-left corner: an uploaded image, served from
-- /api/v1/site/logo, or an external URL. logo_version changes with each
-- upload so browsers can keep the image cached for good.
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_url text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_image bytea;
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_content_type text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS logo_version text NOT NULL DEFAULT '';
