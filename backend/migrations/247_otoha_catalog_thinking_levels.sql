-- TASK-70: per catalog model, the thinking settings it accepts ({canBeOff, settings:[{value, maxEffort}]}; NULL
-- when not known) and its profile per thinking level ([{setting, effort, complexity, extraOutput}]; NULL when not
-- known). The admin edits both.
ALTER TABLE otoha_catalog_models
    ADD COLUMN IF NOT EXISTS thinking JSONB,
    ADD COLUMN IF NOT EXISTS levels JSONB;

COMMENT ON COLUMN otoha_catalog_models.thinking IS
    'Thinking settings the model accepts, {canBeOff, settings}; NULL when not known';
COMMENT ON COLUMN otoha_catalog_models.levels IS
    'Profile per thinking level, [{setting, effort, complexity, extraOutput}]; NULL when not known';

-- Seed from Anthropic's documentation, the per-model table of
-- https://platform.claude.com/docs/en/build-with-claude/thinking (checked 2026-10-12). "maxEffort": "high" is the
-- docs' "at high effort or below" (a 400 error at xhigh or max). Only Anthropic model ids named there, with or
-- without a date suffix; other vendors' models stay NULL (not known) and levels stay empty (the docs give no
-- per-level profile).
UPDATE otoha_catalog_models SET thinking = CASE
    WHEN model_id ~ '^claude-opus-5-5(-[0-9]{8})?$' OR model_id ~ '^claude-(fable|mythos)-5(-1)?(-[0-9]{8})?$'
        THEN '{"canBeOff":false,"settings":[{"value":"adaptive"}]}'
    WHEN model_id ~ '^claude-sonnet-5-5(-[0-9]{8})?$'
        THEN '{"canBeOff":false,"settings":[{"value":"between_tools","maxEffort":"high"},{"value":"adaptive"}]}'
    WHEN model_id ~ '^claude-(haiku-5-5|opus-5)(-[0-9]{8})?$'
        THEN '{"canBeOff":true,"settings":[{"value":"off","maxEffort":"high"},{"value":"adaptive"}]}'
    WHEN model_id ~ '^claude-(sonnet-5|opus-4-[78])(-[0-9]{8})?$'
        THEN '{"canBeOff":true,"settings":[{"value":"off"},{"value":"adaptive"}]}'
    WHEN model_id ~ '^claude-mythos-preview(-[0-9]{8})?$'
        THEN '{"canBeOff":false,"settings":[{"value":"adaptive"},{"value":"budget"}]}'
    WHEN model_id ~ '^claude-(opus|sonnet)-4-6(-[0-9]{8})?$'
        THEN '{"canBeOff":true,"settings":[{"value":"off"},{"value":"adaptive"},{"value":"budget"}]}'
    WHEN model_id ~ '^claude-(opus-4-5|sonnet-4-5|haiku-4-5)(-[0-9]{8})?$'
        THEN '{"canBeOff":true,"settings":[{"value":"off"},{"value":"budget"}]}'
END::jsonb
WHERE thinking IS NULL
  AND model_id ~ '^claude-(opus-5-5|(fable|mythos)-5(-1)?|sonnet-5-5|haiku-5-5|opus-5|sonnet-5|opus-4-[5678]|sonnet-4-[56]|haiku-4-5|mythos-preview)(-[0-9]{8})?$';
