-- Who closed a listing: the seller, staff, the system (the instance stopped
-- being sellable) or expiry (the instance's paid time ran out while
-- listed). Closings the seller did not do are mailed to the seller.
ALTER TABLE service_listings ADD COLUMN IF NOT EXISTS cancelled_by text NOT NULL DEFAULT 'seller';
ALTER TABLE service_listings DROP CONSTRAINT IF EXISTS service_listings_cancelled_by_check;
ALTER TABLE service_listings ADD CONSTRAINT service_listings_cancelled_by_check CHECK (cancelled_by IN ('seller','staff','system','expiry'));
