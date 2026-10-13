-- TASK-76: image entries made from the group's accounts. `source` says who made the entry (manual: the admin, auto:
-- found from the accounts); `manual_fields` lists the fields the admin changed on an auto entry, which a later check
-- of the accounts leaves alone; `unavailable_since` is when the accounts stopped listing the model (NULL while they
-- list it). Existing entries were added by the admin, so they are manual.
ALTER TABLE otoha_catalog_image_models
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS manual_fields JSONB,
    ADD COLUMN IF NOT EXISTS unavailable_since TIMESTAMPTZ;

COMMENT ON COLUMN otoha_catalog_image_models.source IS
    'manual: added by the admin; auto: made from the group accounts (TASK-76)';
COMMENT ON COLUMN otoha_catalog_image_models.manual_fields IS
    'Fields the admin changed on an auto entry; a refresh from the accounts leaves them';
COMMENT ON COLUMN otoha_catalog_image_models.unavailable_since IS
    'When the group accounts stopped listing the model; NULL while they list it';
