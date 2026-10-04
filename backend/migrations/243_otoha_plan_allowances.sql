-- TASK-57: each plan has its own daily, weekly and monthly allowance; a subscription keeps the plan and the
-- allowance it was bought with. Empty means "use the group's limit", so existing plans and subscriptions are unchanged.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);

COMMENT ON COLUMN subscription_plans.monthly_limit_usd IS
    'Plan allowance per 30-day window in USD; NULL uses the group''s monthly_limit_usd (same for daily/weekly)';

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS plan_id BIGINT,
    ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);

COMMENT ON COLUMN user_subscriptions.plan_id IS
    'Plan the current term was bought with (no foreign key: plans are hard-deleted); NULL for subscriptions not bought as a plan';
COMMENT ON COLUMN user_subscriptions.monthly_limit_usd IS
    'Allowance recorded from the plan at purchase; NULL uses the group''s limit (same for daily/weekly)';
