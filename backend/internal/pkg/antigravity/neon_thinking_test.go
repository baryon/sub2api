//go:build unit

package antigravity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNeonAndHydrogenGenerationThinkingLevels(t *testing.T) {
	for _, model := range []string{"gemini-4-neon", "gemini-4-hydrogen"} {
		require.True(t, IsGeminiReasoningModel(model), model)
		for _, tc := range []struct {
			name, body, level string
			thoughts          bool
		}{
			{"default", `{}`, "", false},
			{"low", `{"output_config":{"effort":"low"}}`, "low", false},
			{"medium", `{"output_config":{"effort":"medium"}}`, "medium", false},
			{"high", `{"output_config":{"effort":"high"}}`, "high", false},
			{"xhigh", `{"output_config":{"effort":"xhigh"}}`, "xhigh", false},
			{"max maps to xhigh", `{"output_config":{"effort":"max"}}`, "xhigh", false},
			{"disabled wins", `{"thinking":{"type":"disabled"},"output_config":{"effort":"xhigh"}}`, "low", false},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				var req ClaudeRequest
				require.NoError(t, json.Unmarshal([]byte(tc.body), &req))
				req.Model = model
				req.MaxTokens = 1000
				config := buildGenerationConfig(&req)
				data, err := json.Marshal(config)
				require.NoError(t, err)
				require.NotContains(t, string(data), "thinkingBudget")
				if tc.level == "" && !tc.thoughts {
					require.Nil(t, config.ThinkingConfig)
					return
				}
				require.NotNil(t, config.ThinkingConfig)
				require.Equal(t, tc.level, config.ThinkingConfig.ThinkingLevel)
				require.Equal(t, tc.thoughts, config.ThinkingConfig.IncludeThoughts)
			})
		}
	}

	for _, tiered := range []string{
		"gemini-4-neon-low", "gemini-4-neon-medium", "gemini-4-neon-high", "gemini-4-neon-xhigh",
		"gemini-4-hydrogen-xhigh", "gemini-4-hydrogen-tiered-high", "gemini-4-hydrogen-tiered-xhigh",
	} {
		cfg := buildGenerationConfig(&ClaudeRequest{
			Model:        tiered,
			Thinking:     &ThinkingConfig{Type: "enabled", BudgetTokens: 32768},
			OutputConfig: &ClaudeOutputConfig{Effort: "low"},
		})
		require.NotNil(t, cfg.ThinkingConfig, tiered)
		require.True(t, cfg.ThinkingConfig.IncludeThoughts, tiered)
		require.Empty(t, cfg.ThinkingConfig.ThinkingLevel, tiered)
		require.Zero(t, cfg.ThinkingConfig.ThinkingBudget, tiered)
	}
}
