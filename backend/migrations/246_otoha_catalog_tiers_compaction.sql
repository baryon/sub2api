-- TASK-67: per catalog model, the admin's price tiers by prompt length (empty derives them from billing's
-- long-context rules) and whether the model service compacts a conversation itself.
ALTER TABLE otoha_catalog_models
    ADD COLUMN IF NOT EXISTS price_tiers JSONB,
    ADD COLUMN IF NOT EXISTS compaction_kind VARCHAR(16) NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS compaction_min_trigger INTEGER NOT NULL DEFAULT 0;

COMMENT ON COLUMN otoha_catalog_models.price_tiers IS
    'Admin''s price tiers by prompt length (sale prices); NULL derives them from billing';
COMMENT ON COLUMN otoha_catalog_models.compaction_kind IS
    'Whether the model service compacts a conversation itself: none, anthropic or openai';
COMMENT ON COLUMN otoha_catalog_models.compaction_min_trigger IS
    'Smallest input-token trigger the service takes for compaction; 0 when none or unknown';

-- Seed from Anthropic's documentation (platform.claude.com/docs/en/build-with-claude/compaction-threshold,
-- checked 2026-10-12): server-side compaction is listed for these models and the trigger must be at least
-- 50,000 input tokens. Only Anthropic model ids named there, with or without a date suffix; nothing else is guessed.
UPDATE otoha_catalog_models
SET compaction_kind = 'anthropic', compaction_min_trigger = 50000
WHERE compaction_kind = 'none'
  AND model_id ~ '^claude-(fable-5(-1)?|mythos-5(-1)?|mythos-preview|opus-5(-5)?|opus-4-[678]|sonnet-5(-5)?|sonnet-4-6|haiku-5-5)(-[0-9]{8})?$';
