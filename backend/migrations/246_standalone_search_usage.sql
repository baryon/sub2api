-- Standalone search receipts are intentionally separate from account-backed
-- usage_logs. Existing usage dashboards do not include this ledger.
-- Search has no model-platform attribution; user-platform quota counters do not
-- include these charges. User balance/subscription and API-key quotas do.
CREATE TABLE IF NOT EXISTS standalone_search_usage (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(128) NOT NULL,
    request_fingerprint VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    subscription_id BIGINT REFERENCES user_subscriptions(id),
    provider VARCHAR(128) NOT NULL,
    payload_hash VARCHAR(128) NOT NULL,
    amount_usd NUMERIC(20,8) NOT NULL CHECK (amount_usd >= 0),
    unit_price_usd NUMERIC NOT NULL CHECK (unit_price_usd >= 0),
    rate_multiplier NUMERIC NOT NULL CHECK (rate_multiplier >= 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    calls INTEGER NOT NULL DEFAULT 1 CHECK (calls = 1),
    billing_source VARCHAR(16) NOT NULL CHECK (billing_source IN ('balance', 'subscription')),
    inbound_endpoint TEXT NOT NULL DEFAULT '',
    upstream_endpoint TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    ip_address TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (api_key_id, request_id),
    CHECK ((billing_source = 'subscription') = (subscription_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS idx_standalone_search_usage_user_created
    ON standalone_search_usage (user_id, created_at);
