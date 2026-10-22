package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func diagnosticsFields(d *antigravityStreamDiagnostics) map[string]any {
	fields := d.fields()
	result := make(map[string]any, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		if key, ok := fields[i].(string); ok {
			result[key] = fields[i+1]
		}
	}
	return result
}

func TestAntigravityStreamDiagnosticsWrappedAndDirect(t *testing.T) {
	var d antigravityStreamDiagnostics
	d.observe(": heartbeat")
	d.observe("")
	d.observe("event: message")
	d.observe(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"visible"},{"thought":true,"text":"private"},{"functionCall":{"name":"tool","args":{}}},{"inlineData":{"data":"image"}},{"thoughtSignature":"signature","text":""}]},"finishReason":"STOP"},{"content":{"parts":[{"text":"alternate"}]}}],"usageMetadata":{"promptTokenCount":21,"candidatesTokenCount":8,"thoughtsTokenCount":4,"cachedContentTokenCount":3,"totalTokenCount":33}}}`)
	d.observe(`data: {"candidates":[{"content":{"parts":[{"text":"again"}]}}]}`)
	d.observe("data: [DONE]")
	f := diagnosticsFields(&d)
	for key, want := range map[string]int64{
		"data_lines": 3, "comment_lines": 1, "other_lines": 1, "done_lines": 1, "wrapped_frames": 1, "direct_frames": 1,
		"first_candidate_parts": 6, "other_candidate_parts": 1, "text_parts": 3, "thought_parts": 1, "function_call_parts": 1, "inline_data_parts": 1, "signature_only_parts": 1,
		"prompt_tokens": 21, "output_tokens": 8, "thought_tokens": 4, "cached_tokens": 3, "total_tokens": 33,
	} {
		require.Equal(t, want, f["upstream_"+key], key)
	}
	require.Equal(t, "wrapped", f["upstream_first_data_shape"])
	require.Equal(t, "done", f["upstream_last_data_shape"])
	require.Equal(t, "STOP", f["upstream_finish_reason"])
}

func TestAntigravityStreamDiagnosticsPrivacyAndMalformed(t *testing.T) {
	var d antigravityStreamDiagnostics
	d.observe(`data: {bad private-canary`)
	d.observe(`data: [{"candidates":[{"content":{"parts":[{"text":"private-canary"}]}}]}]`)
	d.observe(`data: {"private-canary":"private-canary"}`)
	d.observe(`data: {"response":{"promptFeedback":{"blockReason":"private-canary"},"candidates":[{"finishReason":"private-canary","content":{"parts":[{"text":"private-canary"},{"thoughtSignature":"private-canary"},{"functionCall":{"name":"private-canary","args":{"secret":"private-canary"}}},{"inlineData":{"mimeType":"private-canary","data":"private-canary"}}]}}]},"error":{"code":503,"message":"private-canary","status":"private-canary"}}`)
	f := diagnosticsFields(&d)
	for key, want := range map[string]int64{"invalid_json_lines": 1, "array_frames": 1, "unknown_frames": 1, "error_frames": 1, "error_code": 503} {
		require.Equal(t, want, f["upstream_"+key], key)
	}
	require.Equal(t, "OTHER", f["upstream_finish_reason"])
	require.Equal(t, "OTHER", f["upstream_block_reason"])
	require.Equal(t, "invalid_json", f["upstream_first_data_shape"])
	require.Equal(t, "wrapped", f["upstream_last_data_shape"])
	b, err := json.Marshal(f)
	require.NoError(t, err)
	require.NotContains(t, string(b), "private-canary")
	require.Less(t, len(b), 2200)
}

func TestAntigravityStreamDiagnosticsSafetyAndUsageSnapshots(t *testing.T) {
	var d antigravityStreamDiagnostics
	d.observe(`data: {"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":14,"candidatesTokenCount":0}}`)
	d.observe(`data: {"response":{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"thought":true,"text":"reasoning"},{"text":""},{"thoughtSignature":"secret"}]}},{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":14,"candidatesTokenCount":3}}}`)
	d.observe(`data: {"usageMetadata":{"promptTokenCount":-8,"candidatesTokenCount":1,"totalTokenCount":1000000000001,"thoughtsTokenCount":"123","cachedContentTokenCount":1.5}}`)
	f := diagnosticsFields(&d)
	require.Equal(t, "SAFETY", f["upstream_block_reason"])
	require.Equal(t, "MAX_TOKENS", f["upstream_finish_reason"])
	require.Equal(t, int64(14), f["upstream_prompt_tokens"])
	require.Equal(t, int64(3), f["upstream_output_tokens"])
	require.Equal(t, int64(0), f["upstream_total_tokens"])
	require.Equal(t, int64(0), f["upstream_thought_tokens"])
	require.Equal(t, int64(0), f["upstream_cached_tokens"])
	require.Equal(t, int64(0), f["upstream_text_parts"])
	require.Equal(t, int64(1), f["upstream_thought_parts"])
	require.Equal(t, int64(1), f["upstream_signature_only_parts"])
}

func TestAntigravityStreamDiagnosticsErrorAndRootShapes(t *testing.T) {
	for _, tc := range []struct {
		payload, shape string
		errorCode      int64
	}{
		{`{"error":{"code":429,"message":"sensitive"}}`, "error", 429},
		{`{"error":{"code":"503"}}`, "error", 0},
		{`{"error":{"code":999999}}`, "error", 0},
		{`{"error":{"code":503.5}}`, "error", 0},
		{`{"response":{"error":{"code":503}}}`, "wrapped", 503},
		{`[]`, "array", 0}, {`null`, "unknown", 0}, {`"sensitive"`, "unknown", 0}, {`{}`, "unknown", 0}, {``, "invalid_json", 0},
	} {
		t.Run(tc.payload, func(t *testing.T) {
			var d antigravityStreamDiagnostics
			d.observe("data:" + tc.payload)
			f := diagnosticsFields(&d)
			require.Equal(t, tc.shape, f["upstream_first_data_shape"])
			require.Equal(t, tc.shape, f["upstream_last_data_shape"])
			require.Equal(t, tc.errorCode, f["upstream_error_code"])
		})
	}
}

func TestAntigravityStreamDiagnosticsCountsLineBytesWithoutPayload(t *testing.T) {
	var d antigravityStreamDiagnostics
	lines := []string{": keepalive", `data: {"usageMetadata":{"promptTokenCount":42}}`, "data: [DONE]", "event: message", ""}
	var want int64
	for _, line := range lines {
		d.observe(line)
		want += int64(len(line))
	}
	f := diagnosticsFields(&d)
	require.Equal(t, want, f["upstream_line_bytes"])
}
