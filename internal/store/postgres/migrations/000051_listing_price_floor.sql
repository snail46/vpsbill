-- Instances may be listed in the trading market from 0.01 (one minor
-- unit) instead of 1.00.
ALTER TABLE service_listings DROP CONSTRAINT IF EXISTS service_listings_price_minor_check;
ALTER TABLE service_listings ADD CONSTRAINT service_listings_price_minor_check CHECK (price_minor BETWEEN 1 AND 10000000);
