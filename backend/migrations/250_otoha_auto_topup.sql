-- Automatic top-up for Otoha users (TASK-75). Off by default; the user turns it on after saving one card.
-- otoha_auto_topups holds one row per user: the settings, the saved card (brand and last four digits only; the
-- number itself stays with Stripe) and why the system turned it off, if it did.
CREATE TABLE IF NOT EXISTS otoha_auto_topups (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    threshold_usd NUMERIC(20,2) NOT NULL DEFAULT 5,
    amount_usd NUMERIC(20,2) NOT NULL DEFAULT 20,
    monthly_cap_usd NUMERIC(20,2) NOT NULL DEFAULT 100,
    provider_instance_id VARCHAR(64) NOT NULL DEFAULT '',
    stripe_customer_id VARCHAR(64) NOT NULL DEFAULT '',
    stripe_payment_method_id VARCHAR(64) NOT NULL DEFAULT '',
    card_brand VARCHAR(32) NOT NULL DEFAULT '',
    card_last4 VARCHAR(4) NOT NULL DEFAULT '',
    card_exp_month SMALLINT NOT NULL DEFAULT 0,
    card_exp_year SMALLINT NOT NULL DEFAULT 0,
    disabled_reason VARCHAR(64) NOT NULL DEFAULT '',
    disabled_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One row per charge attempt. The partial unique index is the guard: a user can have only one pending charge, so
-- two requests that both see a low balance at the same moment still lead to one charge.
CREATE TABLE IF NOT EXISTS otoha_auto_topup_charges (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE SET NULL,
    amount_usd NUMERIC(20,2) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    reason VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ NULL,
    CONSTRAINT otoha_auto_topup_charges_status_check CHECK (status IN ('pending', 'succeeded', 'failed', 'requires_action'))
);

CREATE UNIQUE INDEX IF NOT EXISTS otoha_auto_topup_charges_one_pending
    ON otoha_auto_topup_charges (user_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS otoha_auto_topup_charges_user_created
    ON otoha_auto_topup_charges (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS otoha_auto_topup_charges_order_id
    ON otoha_auto_topup_charges (order_id);

COMMENT ON TABLE otoha_auto_topups IS
    'Otoha automatic top-up: settings, saved card (brand and last four digits only) and the reason it was turned off';
COMMENT ON TABLE otoha_auto_topup_charges IS
    'Otoha automatic top-up charge attempts; at most one pending per user; counted against the monthly cap';
