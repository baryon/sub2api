//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-71: Otoha sends Claude Platform server-side compaction (on demand and at a token threshold), inline tool
// changes, mid-conversation reminders and thinking-binding controls. For an API-key account every one of them must
// reach Claude Platform as sent, the compaction block must come back unchanged, and the compaction sampling that
// Claude Platform reports under usage.iterations must be billed.

const (
	nativeBetaOnDemand = "compact-2026-09-04"
	nativeBetaInline   = "inline-tools-2026-09-15"
	nativeBetaReminder = "mid-conversation-system-clear-at-2026-08-21"
	nativeBetaBinding  = "thinking-binding-controls-2026-08-01"
)

// forwardAPIKeyNative sends one request through Forward for an Anthropic API-key account whose upstream answers with
// upstreamBody (SSE when stream is true, JSON otherwise).
func forwardAPIKeyNative(t *testing.T, passthrough, stream bool, body string, beta string, upstreamBody string) (*ForwardResult, *anthropicHTTPUpstreamRecorder, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if beta != "" {
		c.Request.Header.Set("anthropic-beta", beta)
	}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), PlatformAnthropic)
	require.NoError(t, err)

	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"rid-native"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	account := &Account{
		ID: 71, Name: "claude-platform", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "upstream-key", "base_url": "https://api.anthropic.com"},
		Extra:       map[string]any{"anthropic_passthrough": passthrough},
		Status:      StatusActive, Schedulable: true,
	}
	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	return result, upstream, rec
}

const nativeCompactionBlock = `{"type":"compaction","content":"Summary: the user plans a garden.","signature":"SIGNED=="}`

const (
	// What Claude Platform sends for an on-demand compaction request: the block alone, top-level usage zero, the
	// summarizing call under usage.iterations.
	nativeOnDemandJSON = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[` + nativeCompactionBlock + `],"stop_reason":"compaction","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":310,"output_tokens":587,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]}}`

	nativeOnDemandSSE = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":` + nativeCompactionBlock + `}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"compaction","stop_sequence":null},"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":310,"output_tokens":587,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	// Threshold compaction inside an ordinary request: top-level usage is the answer after the summary only, the
	// compaction sampling (with cache reads and writes) is the first iteration.
	nativeThresholdSSE = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":102426,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":6}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"42"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":268,"cache_creation_input_tokens":0,"cache_read_input_tokens":40,"output_tokens":273,"iterations":[` +
		`{"type":"compaction","input_tokens":1000,"output_tokens":203,"cache_read_input_tokens":90000,"cache_creation_input_tokens":11426,"cache_creation":{"ephemeral_5m_input_tokens":11000,"ephemeral_1h_input_tokens":426}},` +
		`{"type":"message","input_tokens":268,"output_tokens":273,"cache_read_input_tokens":40,"cache_creation_input_tokens":0}]}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
)

func TestNativeCompaction_OnDemandRequestAndBlockUnchanged(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"hello"}]},{"role":"user","content":"go on"}],"compaction":{"type":"summarize"}}`
	for _, passthrough := range []bool{false, true} {
		_, upstream, rec := forwardAPIKeyNative(t, passthrough, true, body, nativeBetaOnDemand, nativeOnDemandSSE)
		require.JSONEq(t, body, string(upstream.lastBody), "passthrough=%v", passthrough)
		require.Equal(t, nativeBetaOnDemand, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "passthrough=%v", passthrough)
		require.Contains(t, rec.Body.String(), `"signature":"SIGNED=="`, "passthrough=%v", passthrough)
		require.Contains(t, rec.Body.String(), `"stop_reason":"compaction"`, "passthrough=%v", passthrough)
		require.Contains(t, rec.Body.String(), `"iterations":[`, "usage.iterations must reach the client, passthrough=%v", passthrough)
	}
}

func TestNativeCompaction_SignedBlockIsSentBackAsReceived(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":300,"stream":true,"messages":[{"role":"assistant","content":[` + nativeCompactionBlock + `]},{"role":"user","content":"what did I pick?"}]}`
	for _, passthrough := range []bool{false, true} {
		_, upstream, _ := forwardAPIKeyNative(t, passthrough, true, body, nativeBetaOnDemand, nativeOnDemandSSE)
		require.JSONEq(t, body, string(upstream.lastBody), "passthrough=%v", passthrough)
		require.Equal(t, nativeBetaOnDemand, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "passthrough=%v", passthrough)
	}
}

func TestNativeCompaction_ThresholdRequestUnchanged(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":400,"stream":true,"messages":[{"role":"user","content":"count"}],"context_management":{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":50000},"pause_after_compaction":false}]}}`
	for _, passthrough := range []bool{false, true} {
		_, upstream, _ := forwardAPIKeyNative(t, passthrough, true, body, "compact-2026-01-12", nativeThresholdSSE)
		require.JSONEq(t, body, string(upstream.lastBody), "passthrough=%v", passthrough)
		require.Equal(t, "compact-2026-01-12", getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "passthrough=%v", passthrough)
	}
}

func TestNativeCompaction_InlineToolsRemindersAndBindingUnchanged(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":300,"stream":true,"thinking":{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}},` +
		`"tools":[{"name":"get_time","description":"Get the time.","input_schema":{"type":"object","properties":{}}}],` +
		`"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"text","text":"hi"}]},{"role":"user","content":"add 17 and 25"},` +
		`{"role":"system","content":[{"type":"tool_addition","tool":{"type":"tool_definition","definition":{"name":"add_numbers","description":"Add.","input_schema":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}}}}}}]},` +
		`{"role":"system","content":[{"type":"tool_removal","tool":{"type":"tool_reference","name":"get_time"}}]},` +
		`{"role":"system","clear_at":"next_user_message","content":[{"type":"text","text":"Answer in French."}]}]}`
	beta := nativeBetaInline + "," + nativeBetaReminder + "," + nativeBetaBinding
	for _, passthrough := range []bool{false, true} {
		_, upstream, _ := forwardAPIKeyNative(t, passthrough, true, body, beta, nativeThresholdSSE)
		require.JSONEq(t, body, string(upstream.lastBody), "passthrough=%v", passthrough)
		require.Equal(t, beta, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "passthrough=%v", passthrough)
	}
}

// Claude Platform bills the compaction sampling too, and reports it only under usage.iterations.
func TestNativeCompaction_BillsTheCompactionSampling(t *testing.T) {
	cases := []struct {
		name         string
		stream       bool
		upstream     string
		body         string
		beta         string
		wantInput    int
		wantOutput   int
		wantCacheRd  int
		wantCacheWr  int
		wantCache5m  int
		wantCache1h  int
		wantResponse string
	}{
		{"on demand, stream", true, nativeOnDemandSSE, `{"model":"claude-haiku-5-5","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"hi"}],"compaction":{"type":"summarize"}}`, nativeBetaOnDemand, 310, 587, 0, 0, 0, 0, `"stop_reason":"compaction"`},
		{"on demand, no stream", false, nativeOnDemandJSON, `{"model":"claude-haiku-5-5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"compaction":{"type":"summarize"}}`, nativeBetaOnDemand, 310, 587, 0, 0, 0, 0, `"stop_reason":"compaction"`},
		{"threshold, stream", true, nativeThresholdSSE, `{"model":"claude-haiku-5-5","max_tokens":400,"stream":true,"messages":[{"role":"user","content":"count"}],"context_management":{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":50000}}]}}`, "compact-2026-01-12", 268 + 1000, 273 + 203, 40 + 90000, 11426, 11000, 426, `"text":"42"`},
	}
	for _, tc := range cases {
		for _, passthrough := range []bool{false, true} {
			result, _, rec := forwardAPIKeyNative(t, passthrough, tc.stream, tc.body, tc.beta, tc.upstream)
			require.Contains(t, rec.Body.String(), tc.wantResponse, "%s passthrough=%v", tc.name, passthrough)
			require.Equal(t, tc.wantInput, result.Usage.InputTokens, "%s passthrough=%v input", tc.name, passthrough)
			require.Equal(t, tc.wantOutput, result.Usage.OutputTokens, "%s passthrough=%v output", tc.name, passthrough)
			require.Equal(t, tc.wantCacheRd, result.Usage.CacheReadInputTokens, "%s passthrough=%v cache read", tc.name, passthrough)
			require.Equal(t, tc.wantCacheWr, result.Usage.CacheCreationInputTokens, "%s passthrough=%v cache write", tc.name, passthrough)
			require.Equal(t, tc.wantCache5m, result.Usage.CacheCreation5mTokens, "%s passthrough=%v cache 5m", tc.name, passthrough)
			require.Equal(t, tc.wantCache1h, result.Usage.CacheCreation1hTokens, "%s passthrough=%v cache 1h", tc.name, passthrough)
		}
	}
}

func TestNativeCompaction_ResponsesWithoutIterationsAreBilledAsBefore(t *testing.T) {
	const plain = `{"id":"msg_3","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":3}}`
	body := `{"model":"claude-haiku-5-5","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`
	for _, passthrough := range []bool{false, true} {
		result, _, _ := forwardAPIKeyNative(t, passthrough, false, body, "", plain)
		require.Equal(t, 5, result.Usage.InputTokens)
		require.Equal(t, 2, result.Usage.OutputTokens)
		require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	}
}

// Iterations of another kind (for example the advisor tool's own model) are not compaction and keep their old handling.
func TestNativeCompaction_OnlyCompactionIterationsAreAdded(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":10,"output_tokens":5,"iterations":[{"type":"advisor_message","model":"claude-opus-5-5","input_tokens":900,"output_tokens":90},{"type":"message","input_tokens":10,"output_tokens":5}]}}`))
	usage.foldCompactionIterations()
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}

func TestNativeCompaction_FoldIsAppliedOnce(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(nativeOnDemandJSON))
	usage.foldCompactionIterations()
	usage.foldCompactionIterations()
	require.Equal(t, 310, usage.InputTokens)
	require.Equal(t, 587, usage.OutputTokens)
	require.True(t, gjson.Valid(nativeOnDemandJSON))
}

// OAuth and setup-token accounts keep the usage they always had.
func TestNativeCompaction_OAuthAccountUsageUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-haiku-5-5","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-oauth"}},
		Body:       io.NopCloser(strings.NewReader(nativeThresholdSSE)),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
	require.NoError(t, err)
	require.Equal(t, 268, result.Usage.InputTokens)
	require.Equal(t, 273, result.Usage.OutputTokens)
	require.Equal(t, 40, result.Usage.CacheReadInputTokens)
}

// A stream that ends after the usage table but before message_stop still bills the compaction sampling.
func TestNativeCompaction_InterruptedStreamStillBillsTheSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-haiku-5-5","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"hi"}],"compaction":{"type":"summarize"}}`)), PlatformAnthropic)
	require.NoError(t, err)
	cut := strings.Index(nativeOnDemandSSE, "event: message_stop")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-cut"}},
		Body:       io.NopCloser(strings.NewReader(nativeOnDemandSSE[:cut])),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	account := &Account{
		ID: 72, Name: "claude-platform", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "upstream-key", "base_url": "https://api.anthropic.com"},
		Extra:       map[string]any{"anthropic_passthrough": false},
		Status:      StatusActive, Schedulable: true,
	}
	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 310, result.Usage.InputTokens)
	require.Equal(t, 587, result.Usage.OutputTokens)
}
