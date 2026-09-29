-- Backups may be encrypted with a passphrase, kept sealed with
-- ENCRYPTION_KEY so this server restores its own backups without asking.
ALTER TABLE backup_config ADD COLUMN IF NOT EXISTS encrypt boolean NOT NULL DEFAULT false;
ALTER TABLE backup_config ADD COLUMN IF NOT EXISTS encryption_passphrase_encrypted bytea;
