-- TASK-54: the Otoha model catalog. Per group, the models an Otoha app may choose, with their capabilities, profile
-- The sale price is not stored: it is the token price billing resolves for the model in the group times the rate.
-- A group with no rows has no catalog (GET /v1/models?client=otoha keeps the plain model list).
CREATE TABLE IF NOT EXISTS otoha_catalog_models (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model_id VARCHAR(200) NOT NULL,
    name VARCHAR(200) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    inputs JSONB,
    tools BOOLEAN NOT NULL DEFAULT FALSE,
    context_tokens INTEGER NOT NULL DEFAULT 0,
    max_output_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_levels JSONB,
    default_reasoning VARCHAR(32) NOT NULL DEFAULT '',
    speed VARCHAR(20) NOT NULL DEFAULT '',
    strengths JSONB,
    complexity VARCHAR(20) NOT NULL DEFAULT '',
    roles JSONB,
    uses JSONB,
    profile_source VARCHAR(20) NOT NULL DEFAULT '',
    cost_tier VARCHAR(20) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS otohacatalogmodel_group_id_model_id
    ON otoha_catalog_models (group_id, model_id);

CREATE INDEX IF NOT EXISTS otohacatalogmodel_group_id_sort_order
    ON otoha_catalog_models (group_id, sort_order);

COMMENT ON TABLE otoha_catalog_models IS
    'Otoha model catalog entries per group (TASK-54); read by GET /v1/models?client=otoha';
COMMENT ON COLUMN otoha_catalog_models.cost_tier IS
    'Price tier shown to the app (low, standard, high); empty derives it from the sale price';
