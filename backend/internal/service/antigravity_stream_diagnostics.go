package service

import (
	"math"
	"strings"

	"github.com/tidwall/gjson"
)

// antigravityStreamDiagnostics retains only counters and fixed classifications.
// Never retain payloads or arbitrary upstream strings here: fields are logged at
// completion (success and failure), including responses containing user text and
// tool arguments.
// The stream reader owns this value; it is not safe for concurrent mutation.
type antigravityStreamDiagnostics struct {
	lineBytes                                                            int64
	data, comments, other, done, invalid                                 int64
	wrapped, direct, arrays, unknown, errors                             int64
	firstParts, otherParts                                               int64
	text, thought, functionCall, inlineData, signatureOnly               int64
	promptTokens, outputTokens, thoughtTokens, cachedTokens, totalTokens int64
	errorCode                                                            int64
	firstShape, lastShape, finishReason, blockReason                     string
}

func (d *antigravityStreamDiagnostics) shape(shape string) {
	if d.firstShape == "" {
		d.firstShape = shape
	}
	d.lastShape = shape
}

func (d *antigravityStreamDiagnostics) observe(line string) {
	d.lineBytes += int64(len(line))
	if line == "" {
		return
	}
	if strings.HasPrefix(line, ":") {
		d.comments++
		return
	}
	if !strings.HasPrefix(line, "data:") {
		d.other++
		return
	}
	d.data++
	payload := strings.TrimSpace(line[len("data:"):])
	if payload == "[DONE]" {
		d.done++
		d.shape("done")
		return
	}
	// Valid operates directly on the string, avoiding a copy of large image data.
	if !gjson.Valid(payload) {
		d.invalid++
		d.shape("invalid_json")
		return
	}
	root := gjson.Parse(payload)
	if root.IsArray() {
		d.arrays++
		d.shape("array")
		return
	}
	if !root.IsObject() {
		d.unknown++
		d.shape("unknown")
		return
	}
	d.observeError(root)
	response := root.Get("response")
	if response.IsObject() {
		d.wrapped++
		d.shape("wrapped")
		d.observeError(response)
	} else if root.Get("candidates").Exists() || root.Get("usageMetadata").Exists() || root.Get("promptFeedback").Exists() {
		d.direct++
		d.shape("direct")
		response = root
	} else if root.Get("error").Exists() {
		d.shape("error")
		return
	} else {
		d.unknown++
		d.shape("unknown")
		return
	}

	if reason := response.Get("promptFeedback.blockReason"); reason.Exists() {
		d.blockReason = antigravityDiagnosticBlockReason(reason.String())
	}
	index := 0
	response.Get("candidates").ForEach(func(_, candidate gjson.Result) bool {
		if reason := candidate.Get("finishReason"); reason.Exists() {
			// The converter consumes the first candidate; alternate reasons must not
			// overwrite its termination reason.
			if index == 0 {
				d.finishReason = antigravityDiagnosticFinishReason(reason.String())
			}
		}
		candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
			if index == 0 {
				d.firstParts++
			} else {
				d.otherParts++
			}
			hasText := part.Get("text").Type == gjson.String && part.Get("text").Str != ""
			isThought := part.Get("thought").Type == gjson.True
			call := part.Get("functionCall").IsObject()
			inline := part.Get("inlineData").IsObject()
			if hasText {
				if isThought {
					d.thought++
				} else {
					d.text++
				}
			}
			if call {
				d.functionCall++
			}
			if inline {
				d.inlineData++
			}
			if !hasText && !call && !inline && part.Get("thoughtSignature").Exists() {
				d.signatureOnly++
			}
			return true
		})
		index++
		return true
	})
	usage := response.Get("usageMetadata")
	antigravityDiagnosticTokenMax(&d.promptTokens, usage.Get("promptTokenCount"))
	antigravityDiagnosticTokenMax(&d.outputTokens, usage.Get("candidatesTokenCount"))
	antigravityDiagnosticTokenMax(&d.thoughtTokens, usage.Get("thoughtsTokenCount"))
	antigravityDiagnosticTokenMax(&d.cachedTokens, usage.Get("cachedContentTokenCount"))
	antigravityDiagnosticTokenMax(&d.totalTokens, usage.Get("totalTokenCount"))
}

func (d *antigravityStreamDiagnostics) observeError(root gjson.Result) {
	e := root.Get("error")
	if !e.Exists() {
		return
	}
	d.errors++
	code := e.Get("code")
	if code.Type == gjson.Number && code.Num >= 100 && code.Num <= 599 && math.Trunc(code.Num) == code.Num {
		d.errorCode = int64(code.Num)
	}
}

// Usage frames contain cumulative counters, so repeated frames must not add
// tokens. Invalid or unreasonable values cannot pollute diagnostic fields.
func antigravityDiagnosticTokenMax(dst *int64, value gjson.Result) {
	if value.Type == gjson.Number && value.Num >= 0 && value.Num <= 1e12 && math.Trunc(value.Num) == value.Num && int64(value.Num) > *dst {
		*dst = int64(value.Num)
	}
}

func antigravityDiagnosticFinishReason(reason string) string {
	switch reason {
	case "FINISH_REASON_UNSPECIFIED", "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "IMAGE_RECITATION", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE":
		// Do not retain a substring backed by the complete upstream payload.
		return strings.Clone(reason)
	default:
		return "OTHER"
	}
}

func antigravityDiagnosticBlockReason(reason string) string {
	switch reason {
	case "BLOCK_REASON_UNSPECIFIED", "SAFETY", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "IMAGE_SAFETY":
		// Do not retain a substring backed by the complete upstream payload.
		return strings.Clone(reason)
	default:
		return "OTHER"
	}
}

func (d *antigravityStreamDiagnostics) fields() []any {
	return []any{
		"upstream_line_bytes", d.lineBytes,
		"upstream_data_lines", d.data, "upstream_comment_lines", d.comments, "upstream_other_lines", d.other,
		"upstream_done_lines", d.done, "upstream_invalid_json_lines", d.invalid,
		"upstream_first_data_shape", d.firstShape, "upstream_last_data_shape", d.lastShape,
		"upstream_wrapped_frames", d.wrapped, "upstream_direct_frames", d.direct, "upstream_array_frames", d.arrays, "upstream_unknown_frames", d.unknown,
		"upstream_error_frames", d.errors, "upstream_error_code", d.errorCode,
		"upstream_first_candidate_parts", d.firstParts, "upstream_other_candidate_parts", d.otherParts,
		"upstream_text_parts", d.text, "upstream_thought_parts", d.thought, "upstream_function_call_parts", d.functionCall, "upstream_inline_data_parts", d.inlineData, "upstream_signature_only_parts", d.signatureOnly,
		"upstream_prompt_tokens", d.promptTokens, "upstream_output_tokens", d.outputTokens, "upstream_thought_tokens", d.thoughtTokens, "upstream_cached_tokens", d.cachedTokens, "upstream_total_tokens", d.totalTokens,
		"upstream_finish_reason", d.finishReason, "upstream_block_reason", d.blockReason,
	}
}
