//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TASK-86: the Messages-format reply built from Gemini must carry the full usage
// (input minus cached, cache reads, output including thoughts) so that clients
// can cost the reply the same way billing does.

type claudeUsageJSON struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

func requireUsage(t *testing.T, got claudeUsageJSON, input, cacheRead, output int) {
	t.Helper()
	require.NotNil(t, got.InputTokens, "input_tokens missing")
	require.NotNil(t, got.CacheReadInputTokens, "cache_read_input_tokens missing")
	require.NotNil(t, got.OutputTokens, "output_tokens missing")
	require.Equal(t, input, *got.InputTokens, "input_tokens")
	require.Equal(t, cacheRead, *got.CacheReadInputTokens, "cache_read_input_tokens")
	require.Equal(t, output, *got.OutputTokens, "output_tokens")
}

func runGeminiMessagesStream(t *testing.T, upstream string) (start, delta claudeUsageJSON, result *geminiStreamResult, events []string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstream)),
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	svc := &GeminiMessagesCompatService{}
	result, err := svc.handleStreamingResponse(c, resp, time.Now(), "gemini-3.5-flash")
	require.NoError(t, err)
	require.NotNil(t, result)

	for _, chunk := range strings.Split(rec.Body.String(), "\n\n") {
		var name, data string
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if name == "" {
			continue
		}
		events = append(events, name)
		switch name {
		case "message_start":
			var p struct {
				Message struct {
					Usage claudeUsageJSON `json:"usage"`
				} `json:"message"`
			}
			require.NoError(t, json.Unmarshal([]byte(data), &p))
			start = p.Message.Usage
		case "message_delta":
			var p struct {
				Usage claudeUsageJSON `json:"usage"`
			}
			require.NoError(t, json.Unmarshal([]byte(data), &p))
			delta = p.Usage
		}
	}
	return start, delta, result, events
}

func TestGeminiMessagesStream_UsageInEveryChunk_WithCache(t *testing.T) {
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}],"usageMetadata":{"promptTokenCount":19570,"cachedContentTokenCount":17739}}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":19570,"cachedContentTokenCount":17739,"candidatesTokenCount":100,"thoughtsTokenCount":50}}` + "\n\n"

	start, delta, result, events := runGeminiMessagesStream(t, upstream)

	require.Equal(t, "message_start", events[0])
	// Prompt and cache reads are already known in the first chunk.
	require.NotNil(t, start.InputTokens)
	require.Equal(t, 1831, *start.InputTokens)
	require.NotNil(t, start.CacheReadInputTokens)
	require.Equal(t, 17739, *start.CacheReadInputTokens)
	require.NotNil(t, start.CacheCreationInputTokens)
	requireUsage(t, delta, 1831, 17739, 150)

	// Billing keeps using the same numbers.
	require.Equal(t, 1831, result.usage.InputTokens)
	require.Equal(t, 17739, result.usage.CacheReadInputTokens)
	require.Equal(t, 150, result.usage.OutputTokens)
}

func TestGeminiMessagesStream_UsageOnlyInLastChunk_WithCache(t *testing.T) {
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2068,"cachedContentTokenCount":1000,"candidatesTokenCount":10,"thoughtsTokenCount":5}}` + "\n\n"

	start, delta, _, _ := runGeminiMessagesStream(t, upstream)

	// Not known yet when the message starts: zero, and completed in message_delta.
	require.NotNil(t, start.InputTokens)
	require.Equal(t, 0, *start.InputTokens)
	requireUsage(t, delta, 1068, 1000, 15)
}

func TestGeminiMessagesStream_UsageInEveryChunk_NoCache(t *testing.T) {
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"Hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"candidatesTokenCount":7}}` + "\n\n"

	start, delta, _, _ := runGeminiMessagesStream(t, upstream)

	require.NotNil(t, start.InputTokens)
	require.Equal(t, 42, *start.InputTokens)
	require.NotNil(t, start.CacheReadInputTokens)
	require.Equal(t, 0, *start.CacheReadInputTokens)
	requireUsage(t, delta, 42, 0, 7)
}

func TestGeminiMessagesStream_NoUsageAtAll(t *testing.T) {
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"Hi"}]},"finishReason":"STOP"}]}` + "\n\n"

	_, delta, _, events := runGeminiMessagesStream(t, upstream)

	require.Equal(t, "message_start", events[0])
	require.Equal(t, "message_stop", events[len(events)-1])
	requireUsage(t, delta, 0, 0, 0)
}

func TestGeminiMessagesNonStream_UsageIncludesCacheAndThoughts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":19570,"cachedContentTokenCount":17739,"candidatesTokenCount":100,"thoughtsTokenCount":50}}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := &GeminiMessagesCompatService{}
	usage, err := svc.handleNonStreamingResponse(c, resp, "gemini-3.5-flash")
	require.NoError(t, err)
	require.Equal(t, 1831, usage.InputTokens)

	var out struct {
		Usage claudeUsageJSON `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	requireUsage(t, out.Usage, 1831, 17739, 150)
	require.NotNil(t, out.Usage.CacheCreationInputTokens)
}

func TestGeminiMessagesNonStream_UsageWithoutCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"candidatesTokenCount":7}}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := &GeminiMessagesCompatService{}
	_, err := svc.handleNonStreamingResponse(c, resp, "gemini-3.5-flash")
	require.NoError(t, err)

	var out struct {
		Usage claudeUsageJSON `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	requireUsage(t, out.Usage, 42, 0, 7)
}
