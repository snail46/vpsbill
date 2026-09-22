ALTER TABLE system_settings
    ADD COLUMN payment_gateway_type text NOT NULL DEFAULT 'disabled',
    ADD COLUMN payment_gateway_config_encrypted bytea;

UPDATE system_settings
SET payment_gateway_type = CASE WHEN payment_checkout_url <> '' THEN 'generic' ELSE 'disabled' END;

ALTER TABLE nodes
    ADD COLUMN provider_type text NOT NULL DEFAULT 'clicd';

CREATE INDEX nodes_provider_status_idx ON nodes(provider_type, status);
