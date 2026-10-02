-- How many discounted instances one account may get from a coupon
-- (0 = no limit), counted like max_uses: paid orders and unpaid orders that
-- can still be paid.
ALTER TABLE coupons ADD COLUMN IF NOT EXISTS per_account_limit integer NOT NULL DEFAULT 0;
