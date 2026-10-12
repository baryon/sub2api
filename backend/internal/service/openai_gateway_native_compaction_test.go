//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-71: Otoha sends OpenAI Responses requests with server-side compaction (`context_management`) and replays the
// compaction items it got back. An API-key account must give OpenAI the same request body the client sent, and
// the response must reach the client with the compaction item and usage unchanged.

const nativeCompactionRequestBody = `{
	"model":"gpt-5.4-nano","stream":true,"store":false,"max_output_tokens":600,
	"context_management":[{"type":"compaction","compact_threshold":1000}],
	"include":["reasoning.encrypted_content"],"reasoning":{"effort":"low"},"prompt_cache_key":"otoha-session-1",
	"tools":[{"type":"function","name":"read_file","description":"Read a file.","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}],
	"input":[
		{"id":"cmp_091ad4fc1a004f86016acc81326d5487d0bb75d28cee58ebc0","type":"compaction","encrypted_content":"gAAAAABqzIEyrrRzTKl6VfTZSYB-ZphJeTcxW68MP5pY0ZG3DweKcrn"},
		{"type":"reasoning","summary":[],"encrypted_content":"gAAAAAB-reasoning"},
		{"type":"function_call","id":"fc_091ad4fc1a004f86016acc82","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.txt\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"hello"},
		{"role":"user","content":[{"type":"input_text","text":"What were we discussing?"}]}
	]
}`

const nativeCompactionResponseSSE = "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"cmp_new\",\"type\":\"compaction\",\"encrypted_content\":\"gAAAAAB-new\"}}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4-nano-2026-03-17\",\"status\":\"completed\",\"output\":[{\"id\":\"cmp_new\",\"type\":\"compaction\",\"encrypted_content\":\"gAAAAAB-new\"}],\"usage\":{\"input_tokens\":2866,\"input_tokens_details\":{\"cached_tokens\":1000},\"output_tokens\":30,\"output_tokens_details\":{\"reasoning_tokens\":10},\"total_tokens\":2896}}}\n\n"

func TestOpenAIAPIKeyNativeCompaction_BodyAndResponseUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(nativeCompactionResponseSSE)),
		}}
		svc := newOpenAIImageGenerationControlTestService(upstream)
		c, rec := newOpenAIImageGenerationControlTestContext(false, "SwiftAgent/1.0")
		account := newOpenAIImageGenerationControlTestAccount()
		account.Extra = map[string]any{"openai_passthrough": passthrough}

		result, err := svc.Forward(context.Background(), c, account, []byte(nativeCompactionRequestBody))
		require.NoError(t, err, "passthrough=%v", passthrough)
		require.NotNil(t, result)
		require.JSONEq(t, nativeCompactionRequestBody, string(upstream.lastBody), "passthrough=%v: the request body must reach OpenAI as sent", passthrough)
		require.Contains(t, rec.Body.String(), `"encrypted_content":"gAAAAAB-new"`, "passthrough=%v: the compaction item must reach the client", passthrough)
		require.Equal(t, 1000, result.Usage.CacheReadInputTokens, "passthrough=%v", passthrough)
		require.Equal(t, 30, result.Usage.OutputTokens, "passthrough=%v", passthrough)
		require.True(t, gjson.GetBytes(upstream.lastBody, "context_management.0.compact_threshold").Exists())
	}
}
