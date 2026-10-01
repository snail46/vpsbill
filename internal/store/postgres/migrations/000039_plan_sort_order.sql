-- Plans are listed by sort_order (smaller first), then newest first, in the
-- admin list and the shop.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS sort_order integer NOT NULL DEFAULT 0;
