ALTER TABLE nodes
    ADD COLUMN provider_options jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN nodes.provider_options IS
    'Non-secret per-node provider settings validated against the provider descriptor; secrets stay in api_key_ciphertext';
