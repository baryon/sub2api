package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapResponsesEffortToAnthropic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "low", want: "low"},
		{in: "MEDIUM", want: "medium"},
		{in: "high", want: "high"},
		{in: "xhigh", want: "xhigh"},
		{in: "max", want: "max"},
		{in: "minimal", want: "low"},
		{in: "none", want: ""},
		{in: "None", want: ""},
		{in: "", want: ""},
		{in: "ultra", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, mapResponsesEffortToAnthropic(tt.in))
		})
	}
}

func TestResponsesToAnthropicRequest_DropsCodexNoneEffort(t *testing.T) {
	t.Parallel()

	req := &ResponsesRequest{
		Model:     "claude-opus-4-6",
		Input:     json.RawMessage(`[{"role":"user","content":"hello"}]`),
		Reasoning: &ResponsesReasoning{Effort: "none"},
	}

	got, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.Nil(t, got.OutputConfig)
	require.Nil(t, got.Thinking)
}

func TestResponsesToAnthropicRequest_KeepsClaudeEffort(t *testing.T) {
	t.Parallel()

	req := &ResponsesRequest{
		Model:     "claude-opus-4-6",
		Input:     json.RawMessage(`[{"role":"user","content":"hello"}]`),
		Reasoning: &ResponsesReasoning{Effort: "high"},
	}

	got, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.NotNil(t, got.OutputConfig)
	require.Equal(t, "high", got.OutputConfig.Effort)
	require.NotNil(t, got.Thinking)
	require.Equal(t, "enabled", got.Thinking.Type)
}
