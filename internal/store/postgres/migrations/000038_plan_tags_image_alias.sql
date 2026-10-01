-- Short labels shown on a plan's card in the shop ("CN2 GIA", "原生 IP").
ALTER TABLE plans ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}';

-- localhost/hatch-alpine:latest is the second name of
-- localhost/hatch-alpine3.22:latest; plans use the versioned one, which the
-- shop shows as "Alpine 3.22". Running instances keep working: the agent
-- still tags both names.
UPDATE plans SET default_template_id='localhost/hatch-alpine3.22:latest'
WHERE default_template_id='localhost/hatch-alpine:latest';
UPDATE plans SET allowed_template_ids = CASE
        WHEN 'localhost/hatch-alpine3.22:latest' = ANY(allowed_template_ids)
            THEN array_remove(allowed_template_ids, 'localhost/hatch-alpine:latest')
        ELSE array_replace(allowed_template_ids, 'localhost/hatch-alpine:latest', 'localhost/hatch-alpine3.22:latest')
    END
WHERE 'localhost/hatch-alpine:latest' = ANY(allowed_template_ids);
