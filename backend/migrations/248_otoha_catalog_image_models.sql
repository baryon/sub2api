-- TASK-73: the image models of a group's Otoha catalog. The app reads them in `imageModels`, apart from the chat
-- models, so older apps do not list them as chat models. price_* are the price of one image in a size tier before
-- the group's rate (the same meaning as the group's own image prices); NULL follows billing (the group's per-model
-- pricing, else the group's 1K/2K/4K prices, else the built-in defaults).
CREATE TABLE IF NOT EXISTS otoha_catalog_image_models (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model_id VARCHAR(200) NOT NULL,
    name VARCHAR(200) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    sizes JSONB,
    qualities JSONB,
    edits BOOLEAN NOT NULL DEFAULT FALSE,
    price_1k DOUBLE PRECISION,
    price_2k DOUBLE PRECISION,
    price_4k DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS otohacatalogimagemodel_group_id_model_id
    ON otoha_catalog_image_models (group_id, model_id);

CREATE INDEX IF NOT EXISTS otohacatalogimagemodel_group_id_sort_order
    ON otoha_catalog_image_models (group_id, sort_order);

COMMENT ON TABLE otoha_catalog_image_models IS
    'Otoha catalog image models per group (TASK-73); read in imageModels of GET /v1/models?client=otoha';
COMMENT ON COLUMN otoha_catalog_image_models.price_1k IS
    'Price of one 1K image before the group rate; NULL follows billing';
