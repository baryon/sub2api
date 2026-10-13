-- TASK-82: a plan's name, description and features per language, e.g.
-- {"en": {"name": "...", "description": "...", "features": "..."}}. A language or field that is missing
-- shows the plan's own text, so existing plans are unchanged until the admin fills a language.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS translations JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN subscription_plans.translations IS
    'Per-language plan text keyed by lower-case language tag: {"en": {"name", "description", "features"}}; missing text falls back to name/description/features';
