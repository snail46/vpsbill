ALTER TABLE services
    ADD COLUMN root_password_ciphertext bytea;

COMMENT ON COLUMN services.root_password_ciphertext IS
    'AES-GCM encrypted current root password; never return outside an owner-authorized endpoint';
