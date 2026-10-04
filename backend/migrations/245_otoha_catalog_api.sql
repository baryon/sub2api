-- TASK-64: the format the Otoha app calls a catalog model in (anthropic-messages, deepseek-responses or
-- openai-responses). Empty, the default for existing entries, derives it from the provider the model is routed to.
ALTER TABLE otoha_catalog_models
    ADD COLUMN IF NOT EXISTS api VARCHAR(32) NOT NULL DEFAULT '';

COMMENT ON COLUMN otoha_catalog_models.api IS
    'Format the app calls the model in (anthropic-messages, deepseek-responses, openai-responses); empty derives it from the routed provider';
