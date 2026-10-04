-- One-time codes the Otoha app exchanges for its configuration (TASK-55). Only the SHA-256 of a code is kept;
-- a code works once, within 15 minutes of its creation.
CREATE TABLE IF NOT EXISTS otoha_claims (
    id BIGSERIAL PRIMARY KEY,
    code_hash VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS otoha_claims_code_hash_key ON otoha_claims (code_hash);
CREATE INDEX IF NOT EXISTS otoha_claims_expires_at_idx ON otoha_claims (expires_at);
CREATE INDEX IF NOT EXISTS otoha_claims_user_id_idx ON otoha_claims (user_id);

COMMENT ON TABLE otoha_claims IS
    'Otoha app claim codes: SHA-256 of the code, the user and key it hands out, 15-minute expiry, single use';
