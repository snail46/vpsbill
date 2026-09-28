-- Stock: how many instances a plan may sell in total (live instances plus
-- units in payable orders). NULL leaves the plan limited only by capacity.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS stock_limit integer CHECK (stock_limit >= 0);

-- A price may be sold a limited number of times (orders at this plan and
-- cycle); NULL is unlimited.
ALTER TABLE plan_prices ADD COLUMN IF NOT EXISTS purchase_limit integer CHECK (purchase_limit >= 1);

-- The list price a service was bought at. Renewals fall back to it when the
-- plan no longer sells the service's cycle, instead of stopping for review.
ALTER TABLE services ADD COLUMN IF NOT EXISTS list_price_minor bigint;

UPDATE services s
SET list_price_minor = coalesce((oi.configuration->>'list_amount_minor')::bigint, oi.unit_amount_minor)
FROM order_items oi
WHERE oi.id = s.order_item_id AND s.list_price_minor IS NULL;
