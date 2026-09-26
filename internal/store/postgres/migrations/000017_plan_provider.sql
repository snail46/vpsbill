-- A plan sells one node backend: template IDs and capabilities differ
-- between providers, so the scheduler only places it on matching nodes.
ALTER TABLE plans ADD COLUMN provider_type text NOT NULL DEFAULT 'clicd';
CREATE INDEX plans_provider_type_idx ON plans(provider_type);
