package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffortLevelsForModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  []string
	}{
		{model: "claude-opus-4-6", want: []string{EffortLow, EffortMedium, EffortHigh, EffortMax}},
		{model: "claude-opus-4-6-thinking", want: []string{EffortLow, EffortMedium, EffortHigh, EffortMax}},
		{model: "anthropic/claude-sonnet-4-6", want: []string{EffortLow, EffortMedium, EffortHigh, EffortMax}},
		{model: "claude-opus-5", want: []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}},
		{model: "anthropic/claude-opus-5.5", want: []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}},
		{model: "claude-sonnet-5", want: []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}},
		{model: "claude-sonnet-5-5", want: []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}},
		{model: "us.anthropic.claude-sonnet-5-5", want: []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}},
		{model: "claude-fable-5", want: []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}},
		{model: "claude-opus-4-5-20251101", want: []string{EffortLow, EffortMedium, EffortHigh}},
		{model: "claude-haiku-4-5-20251001", want: nil},
		{model: "claude-sonnet-4-5", want: nil},
		{model: "gpt-5.6", want: nil},
		{model: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, EffortLevelsForModel(tt.model))
		})
	}
}

func TestIsOpus55OpenRouterExactAlias(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"claude-opus-5-5", "anthropic/claude-opus-5.5"} {
		require.True(t, IsOpus55(model), model)
	}
	for _, model := range []string{"claude-opus-5", "anthropic/claude-opus-5.6", "anthropic/claude-opus-5.5-preview"} {
		require.False(t, IsOpus55(model), model)
	}
}

func TestIsSonnet55(t *testing.T) {
	t.Parallel()
	for _, model := range []string{
		"claude-sonnet-5-5",
		"anthropic/claude-sonnet-5.5",
		"anthropic.claude-sonnet-5-5",
		"us.anthropic.claude-sonnet-5-5",
		"us-gov.anthropic.claude-sonnet-5-5",
		"global.anthropic.claude-sonnet-5-5-thinking",
	} {
		require.True(t, IsSonnet55(model), model)
	}
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5-5-preview", "claude-opus-5-5"} {
		require.False(t, IsSonnet55(model), model)
	}
}

func TestClampEffortForModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{name: "opus 4.6 keeps max", model: "claude-opus-4-6", effort: "max", want: EffortMax},
		{name: "opus 4.6 maps xhigh to max", model: "claude-opus-4-6", effort: "xhigh", want: EffortMax},
		{name: "opus 4.5 maps max to high", model: "claude-opus-4-5-20251101", effort: "max", want: EffortHigh},
		{name: "opus 4.5 maps xhigh to high", model: "claude-opus-4-5", effort: "xhigh", want: EffortHigh},
		{name: "opus 5 keeps xhigh", model: "claude-opus-5", effort: "xhigh", want: EffortXHigh},
		{name: "haiku drops effort", model: "claude-haiku-4-5-20251001", effort: "medium", want: ""},
		{name: "non-claude passthrough", model: "gpt-5.6", effort: "xhigh", want: EffortXHigh},
		{name: "empty effort", model: "claude-opus-4-6", effort: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, ClampEffortForModel(tt.model, tt.effort))
		})
	}
}
