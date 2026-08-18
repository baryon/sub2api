-- Expand composite_model_routes.target_platform to the CN providers.
--
-- Migration 224 on this fork already added deepseek. Official 0.1.178 added
-- kimi/zhipu/deepseek to user_platform_quotas but left composite routes on the
-- original 172-era 5-platform CHECK. DROP + ADD is a superset of both states.
ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                               'kimi', 'zhipu', 'deepseek'));
