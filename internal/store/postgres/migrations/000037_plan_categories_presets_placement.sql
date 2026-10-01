-- Plan categories group platform plans in the admin catalog and the shop.
CREATE TABLE IF NOT EXISTS plan_categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS plan_categories_name_key ON plan_categories (lower(btrim(name)));

-- A deleted category leaves its plans uncategorised.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS category_id uuid REFERENCES plan_categories(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS plans_category_idx ON plans (category_id);

-- How a platform plan picks a node: nodes sells only node_ids, pack fills
-- the fullest node that still fits, spread takes the emptiest. Existing
-- plans keep packing, which is how they were placed before.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS node_selection text NOT NULL DEFAULT 'pack';
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_node_selection_check;
ALTER TABLE plans ADD CONSTRAINT plans_node_selection_check CHECK (node_selection IN ('nodes', 'pack', 'spread'));
ALTER TABLE plans ADD COLUMN IF NOT EXISTS node_ids text[] NOT NULL DEFAULT '{}';

-- Plan presets keep the settings a series of plans shares; the admin form
-- owns their shape.
CREATE TABLE IF NOT EXISTS plan_presets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(settings) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS plan_presets_name_key ON plan_presets (lower(btrim(name)));
