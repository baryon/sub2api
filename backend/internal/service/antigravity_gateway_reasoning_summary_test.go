package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the production Gemini -> Anthropic -> Responses path, including the
// signature-only thinking block that made Otoha reject otherwise valid tool calls.
func TestAntigravityCompatResponsesReasoningSummaryContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, text := range []string{"", "先检查文件"} {
		t.Run("thinking="+text, func(t *testing.T) {
			upstreamSSE := `data: {"response":{"responseId":"resp_summary","candidates":[{"content":{"role":"model","parts":[` +
				`{"thought":true,"text":"` + text + `","thoughtSignature":"sig_think"},` +
				`{"thoughtSignature":"sig_call","functionCall":{"name":"workspace_read","args":{"path":"README.md"},"id":"call_read"}}` +
				`]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":6}}}` + "\n\n"
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			}}}
			svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
			request := []byte(`{"model":"gemini-3.1-pro-high","stream":true,"input":"Read README.md"}`)
			c, recorder := newAntigravityCompatContext(http.MethodPost, "/v1/responses", request)
			_, err := svc.ForwardAsResponses(context.Background(), c, newAntigravityCompatAccount(AccountTypeOAuth), request, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)

			var summaryEvents []string
			var completed gjson.Result
			lastSequence := int64(-1)
			for _, line := range strings.Split(recorder.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				event := gjson.Parse(strings.TrimPrefix(line, "data: "))
				require.True(t, event.Get("sequence_number").Exists())
				require.Greater(t, event.Get("sequence_number").Int(), lastSequence)
				lastSequence = event.Get("sequence_number").Int()
				typ := event.Get("type").String()
				if strings.HasPrefix(typ, "response.reasoning_summary_") {
					summaryEvents = append(summaryEvents, typ)
					require.True(t, event.Get("output_index").Exists())
					require.True(t, event.Get("summary_index").Exists())
					require.EqualValues(t, 0, event.Get("output_index").Int())
					require.EqualValues(t, 0, event.Get("summary_index").Int())
					switch typ {
					case "response.reasoning_summary_text.delta":
						require.Equal(t, text, event.Get("delta").String())
					case "response.reasoning_summary_text.done":
						require.Equal(t, text, event.Get("text").String())
					case "response.reasoning_summary_part.done":
						require.Equal(t, text, event.Get("part.text").String())
					}
				}
				if typ == "response.completed" {
					completed = event.Get("response")
				}
			}
			if text == "" {
				require.Empty(t, summaryEvents, "a signature alone has no summary text to finish")
			} else {
				require.Equal(t, []string{
					"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta",
					"response.reasoning_summary_text.done", "response.reasoning_summary_part.done",
				}, summaryEvents)
			}
			require.Equal(t, "completed", completed.Get("status").String())
			var reasoning, calls int
			for _, item := range completed.Get("output").Array() {
				switch item.Get("type").String() {
				case "reasoning":
					require.True(t, item.Get("summary").IsArray(), "terminal reasoning must carry summary:[] even without text")
					require.True(t, strings.HasPrefix(item.Get("encrypted_content").String(), "gemini-thinking-v1:"))
					if reasoning == 0 && text != "" {
						require.Equal(t, text, item.Get("summary.0.text").String())
					} else {
						require.Empty(t, item.Get("summary").Array())
					}
					reasoning++
				case "function_call":
					require.Equal(t, "workspace_read", item.Get("name").String())
					require.JSONEq(t, `{"path":"README.md"}`, item.Get("arguments").String())
					calls++
				}
			}
			require.Equal(t, 2, reasoning, "thinking and tool signatures must both survive")
			require.Equal(t, 1, calls)
		})
	}
}
