-- Plans may sell custom cycles: "d<N>" for N days or "m<N>" for N months,
-- next to the named ones. The Go code checks the ranges.
ALTER TABLE plan_prices DROP CONSTRAINT IF EXISTS plan_prices_billing_cycle_check;
ALTER TABLE plan_prices ADD CONSTRAINT plan_prices_billing_cycle_check
    CHECK (billing_cycle ~ '^(monthly|quarterly|semiannual|annual|[dm][1-9][0-9]{0,2})$');

ALTER TABLE services DROP CONSTRAINT IF EXISTS services_billing_cycle_check;
ALTER TABLE services ADD CONSTRAINT services_billing_cycle_check
    CHECK (billing_cycle ~ '^(monthly|quarterly|semiannual|annual|[dm][1-9][0-9]{0,2})$');
