package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A completed message's output_text parts carry annotations and logprobs, as the streamed parts already do: strict
// Responses clients (the OtohaAI app's continuation check) reject a message whose output_text has no annotations,
// which broke Claude-through-Responses conversations after a tool call (2026-10-04).
func TestCompletedMessageOutputTextCarriesAnnotations(t *testing.T) {
	out := ResponsesOutput{Type: "message", ID: "item_1", Role: "assistant", Status: "completed",
		Content: []ResponsesContentPart{{Type: "output_text", Text: "你好"}}}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	content := decoded["content"].([]any)
	require.Len(t, content, 1)
	part := content[0].(map[string]any)
	require.Equal(t, "output_text", part["type"])
	require.Equal(t, "你好", part["text"])
	require.Equal(t, []any{}, part["annotations"])
	require.Equal(t, []any{}, part["logprobs"])
	require.Equal(t, "assistant", decoded["role"])
	require.Equal(t, "completed", decoded["status"])

	// The same holds inside a whole response, as sent in response.completed.
	raw, err = json.Marshal(ResponsesResponse{ID: "resp_1", Object: "response", Status: "completed", Output: []ResponsesOutput{out}})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"annotations":[]`)

	// An empty message keeps content:[]; other output types are unchanged.
	raw, err = json.Marshal(ResponsesOutput{Type: "message", ID: "item_2", Role: "assistant", Status: "completed"})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"content":[]`)
	call := ResponsesOutput{Type: "function_call", ID: "fc_1", CallID: "call_1", Name: "get_time", Arguments: "{}", Status: "completed"}
	type alias ResponsesOutput
	want, _ := json.Marshal(alias(call))
	got, err := json.Marshal(call)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}
