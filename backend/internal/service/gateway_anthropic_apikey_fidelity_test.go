//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-69: an Anthropic API-key account (the Otoha group's Claude Platform key) must give the same
// request upstream as the client sent straight to Claude Platform; only the credential changes.
// These tests drive Forward with a fake upstream and look at what it receives.

const fidelityOKResponse = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`

// forwardAPIKeyForFidelity sends one request through Forward for an Anthropic API-key account and returns
// what the fake upstream received. passthrough chooses the account's "replace auth only" switch.
func forwardAPIKeyForFidelity(t *testing.T, passthrough bool, body string, requestHeaders map[string]string) *anthropicHTTPUpstreamRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	for k, v := range requestHeaders {
		c.Request.Header.Set(k, v)
	}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), PlatformAnthropic)
	require.NoError(t, err)

	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"rid-fidelity"}},
		Body:       io.NopCloser(strings.NewReader(fidelityOKResponse)),
	}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	account := &Account{
		ID: 69, Name: "claude-platform", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "upstream-key", "base_url": "https://api.anthropic.com"},
		Extra:       map[string]any{"anthropic_passthrough": passthrough},
		Status:      StatusActive, Schedulable: true,
	}
	_, err = svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	return upstream
}

func TestAPIKeyFidelity_CompactionKeepsContextManagementUnderItsOwnBeta(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":50,"context_management":{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":50000}}]},"messages":[{"role":"user","content":"hi"}]}`
	for _, passthrough := range []bool{false, true} {
		upstream := forwardAPIKeyForFidelity(t, passthrough, body, map[string]string{"anthropic-beta": claude.BetaCompact})
		require.JSONEq(t, body, string(upstream.lastBody), "passthrough=%v: compaction must reach Claude Platform", passthrough)
		require.Equal(t, claude.BetaCompact, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "passthrough=%v", passthrough)
	}
}

func TestAPIKeyFidelity_CompactionFieldStillNeedsABeta(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":50,"context_management":{"edits":[{"type":"compact_20260112"}]},"messages":[{"role":"user","content":"hi"}]}`
	for _, passthrough := range []bool{false, true} {
		upstream := forwardAPIKeyForFidelity(t, passthrough, body, map[string]string{"anthropic-beta": "interleaved-thinking-2025-05-14"})
		require.False(t, gjson.GetBytes(upstream.lastBody, "context_management").Exists(), "passthrough=%v", passthrough)
	}
}

func TestSanitizeAnthropicAPIKeyBodyForBetaTokens_CompactBeta(t *testing.T) {
	body := []byte(`{"context_management":{"edits":[{"type":"compact_20260112"}]},"messages":[]}`)
	for _, beta := range []string{claude.BetaCompact, "a," + claude.BetaCompact + ",b", claude.BetaCompact + "," + claude.BetaContextManagement} {
		out, changed := sanitizeAnthropicAPIKeyBodyForBetaTokens(body, beta)
		require.False(t, changed, beta)
		require.Equal(t, string(body), string(out), beta)
	}
	out, changed := sanitizeAnthropicAPIKeyBodyForBetaTokens(body, "compact-2026-01-12-rev2")
	require.True(t, changed, "a token must match whole")
	require.False(t, gjson.GetBytes(out, "context_management").Exists())

	// Other fields keep their own rules: the compaction beta does not unlock them.
	withFallbacks := []byte(`{"fallbacks":"default","context_management":{"edits":[]},"messages":[]}`)
	out, changed = sanitizeAnthropicAPIKeyBodyForBetaTokens(withFallbacks, claude.BetaCompact)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "fallbacks").Exists())
	require.True(t, gjson.GetBytes(out, "context_management").Exists())
}

func TestSanitizeAnthropicBodyForBetaTokens_OAuthRulesUnchangedForCompactBeta(t *testing.T) {
	// OAuth and setup-token accounts keep the existing rule: only the context-management beta keeps the field.
	body := []byte(`{"context_management":{"edits":[{"type":"compact_20260112"}]},"messages":[]}`)
	out, changed := sanitizeAnthropicBodyForBetaTokens(body, claude.BetaCompact)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "context_management").Exists())
}

const fidelityThinkingHistory = `{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"SIGNED"},{"type":"redacted_thinking","data":"ENCRYPTED"},{"type":"tool_use","id":"toolu_1","name":"t","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}`

func TestAPIKeyFidelity_SignedThinkingAndRedactedThinkingAreSentBack(t *testing.T) {
	tools := `"tools":[{"name":"t","description":"d","input_schema":{"type":"object"}}]`
	for _, thinking := range []string{`"thinking":{"type":"adaptive"}`, `"thinking":{"type":"enabled","budget_tokens":2048}`, `"thinking":{"type":"disabled"}`} {
		body := `{"model":"claude-haiku-5-5","max_tokens":4096,` + thinking + `,` + tools + `,"messages":[` + fidelityThinkingHistory + `]}`
		for _, passthrough := range []bool{false, true} {
			upstream := forwardAPIKeyForFidelity(t, passthrough, body, nil)
			require.JSONEq(t, body, string(upstream.lastBody), "%s passthrough=%v", thinking, passthrough)
		}
	}
}

func TestAPIKeyFidelity_ThinkingWithoutSignatureIsStillDropped(t *testing.T) {
	// A thinking block without a signature is rejected by Claude Platform, so it is not sent.
	body := `{"model":"claude-haiku-5-5","max_tokens":100,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"a"}]},{"role":"user","content":"go"}]}`
	upstream := forwardAPIKeyForFidelity(t, false, body, nil)
	require.Equal(t, "text", gjson.GetBytes(upstream.lastBody, "messages.1.content.0.type").String())
	require.Equal(t, int64(1), gjson.GetBytes(upstream.lastBody, "messages.1.content.#").Int())
}

func TestFilterThinkingBlocksForAPIKey_KeepsEverythingClaudePlatformAccepts(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-5-5","thinking":{"type":"disabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"S"},{"type":"redacted_thinking","data":"D"},{"type":"redacted_thinking","data":""},{"type":"text","text":"a"}]}]}`)
	out := FilterThinkingBlocksForAPIKey(body, "claude-haiku-5-5")
	require.Equal(t, int64(3), gjson.GetBytes(out, "messages.0.content.#").Int())
	require.Equal(t, "thinking", gjson.GetBytes(out, "messages.0.content.0.type").String())
	require.Equal(t, "redacted_thinking", gjson.GetBytes(out, "messages.0.content.1.type").String())
	require.Equal(t, "text", gjson.GetBytes(out, "messages.0.content.2.type").String())

	// OAuth and setup-token accounts keep the existing filter.
	legacy := FilterThinkingBlocks(body, "claude-haiku-5-5")
	require.Equal(t, int64(1), gjson.GetBytes(legacy, "messages.0.content.#").Int())
}

func TestFilterThinkingBlocksForAPIKey_NonClaudeModelsAreLeftAlone(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x"}]}]}`)
	require.Equal(t, string(body), string(FilterThinkingBlocksForAPIKey(body, "deepseek-v4-pro")))
}

func TestAPIKeyFidelity_NoEmptyBetaHeaderWhenEveryTokenIsDropped(t *testing.T) {
	s := newTestGatewayServiceForBeta(false)
	hdr := http.Header{}
	hdr.Set("anthropic-beta", claude.BetaFastMode)
	drop := map[string]struct{}{claude.BetaFastMode: {}}

	beta, set := s.computeFinalAnthropicBeta("apikey", false, "claude-haiku-5-5", hdr, []byte(`{}`), drop)
	require.False(t, set, "an empty anthropic-beta header must not be sent, got %q", beta)

	beta, set = s.computeFinalAnthropicBeta("apikey", false, "claude-haiku-5-5", hdr, []byte(`{}`), nil)
	require.True(t, set)
	require.Equal(t, claude.BetaFastMode, beta)

	// Counting tokens follows the same rule.
	beta, set = s.computeFinalCountTokensAnthropicBeta("apikey", false, "claude-haiku-5-5", hdr, []byte(`{}`), drop)
	require.False(t, set, "got %q", beta)
}

func TestAPIKeyFidelity_ClientHeadersAndBetaTokensGoThroughUnchanged(t *testing.T) {
	body := `{"model":"claude-haiku-5-5","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`
	beta := "context-management-2025-06-27,interleaved-thinking-2025-05-14,some-future-beta-2030-01-01"
	for _, passthrough := range []bool{false, true} {
		upstream := forwardAPIKeyForFidelity(t, passthrough, body, map[string]string{
			"anthropic-beta":    beta,
			"anthropic-version": "2023-06-01",
			"User-Agent":        "otoha-test/1.0",
			"X-Api-Key":         "user-key",
		})
		require.Equal(t, body, string(upstream.lastBody), "passthrough=%v", passthrough)
		require.Equal(t, beta, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "passthrough=%v", passthrough)
		require.Equal(t, "otoha-test/1.0", getHeaderRaw(upstream.lastReq.Header, "user-agent"), "passthrough=%v", passthrough)
		require.Equal(t, "upstream-key", getHeaderRaw(upstream.lastReq.Header, "x-api-key"), "passthrough=%v", passthrough)
		require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "authorization"), "passthrough=%v", passthrough)
	}
}
