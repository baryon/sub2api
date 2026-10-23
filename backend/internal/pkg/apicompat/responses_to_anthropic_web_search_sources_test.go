package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesToAnthropicKeepsWebSearchSources(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_1",
		Status: "completed",
		Output: []ResponsesOutput{{
			Type:   "web_search_call",
			ID:     "ws_1",
			Status: "completed",
			Action: &WebSearchAction{
				Type:  "search",
				Query: "IANA example domains",
				Sources: []WebSearchSource{{
					Type: "url",
					URL:  "https://www.iana.org/domains/reserved",
				}},
			},
		}},
	}

	out := ResponsesToAnthropic(resp, "gpt-6.1-sol")
	require.GreaterOrEqual(t, len(out.Content), 2)
	require.Equal(t, "server_tool_use", out.Content[0].Type)
	require.Equal(t, "web_search", out.Content[0].Name)
	require.Equal(t, "web_search_tool_result", out.Content[1].Type)

	var results []map[string]any
	require.NoError(t, json.Unmarshal(out.Content[1].Content, &results))
	require.Len(t, results, 1)
	require.Equal(t, "web_search_result", results[0]["type"])
	require.Equal(t, "https://www.iana.org/domains/reserved", results[0]["url"])
}
