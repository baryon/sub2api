ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS balance_fallback_enabled BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN groups.balance_fallback_enabled IS
    'Subscription group only: when the user has no usable plan (none, expired or over its limit), charge the balance instead; refuse with CREDIT_EXHAUSTED only when neither is left';
