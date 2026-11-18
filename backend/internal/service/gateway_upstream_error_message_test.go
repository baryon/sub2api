package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractUpstreamErrorMessageSupportsProviderEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
		grok bool
	}{
		{
			name: "openai object message",
			body: `{"error":{"message":"Invalid 'input': expected an array.","type":"invalid_request_error"}}`,
			want: "Invalid 'input': expected an array.",
		},
		{
			name: "claude object message",
			body: `{"type":"error","error":{"type":"invalid_request_error","message":"tools: extra fields not permitted"}}`,
			want: "tools: extra fields not permitted",
		},
		{
			name: "xai string error",
			grok: true,
			body: `{"code":"invalid-argument","error":"This model does not support ` + "`reasoning_effort`" + ` value ` + "`none`" + `."}`,
			want: "This model does not support `reasoning_effort` value `none`.",
		},
		{
			name: "xai nested object without message",
			grok: true,
			body: `{"code":"invalid-argument","error":{"error":"Could not decrypt the provided encrypted_content."}}`,
			want: "Could not decrypt the provided encrypted_content.",
		},
		{
			name: "quoted json inside error.message",
			body: `{"error":{"message":"{\"error\":{\"message\":\"inner openai\"}}"}}`,
			want: "inner openai",
		},
		{
			name: "quoted json inside string error",
			grok: true,
			body: `{"error":"{\"code\":\"invalid-argument\",\"error\":\"Could not decrypt the provided encrypted_content.\"}"}`,
			want: "Could not decrypt the provided encrypted_content.",
		},
		{
			name: "chatgpt detail",
			body: `{"detail":"invalid token"}`,
			want: "invalid token",
		},
		{
			name: "top-level message",
			body: `{"message":"plain failure"}`,
			want: "plain failure",
		},
		{
			name: "empty body",
			body: ``,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var account *Account
			if tt.grok {
				account = &Account{Platform: PlatformGrok}
			}
			require.Equal(t, tt.want, upstreamErrorMessageForAccount([]byte(tt.body), account))
		})
	}
}

func TestExtractUpstreamErrorCodeSupportsXAIFlatCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, "invalid_function_parameters", extractUpstreamErrorCode([]byte(openAIInvalidFunctionParametersBody)))
	require.Equal(t, "invalid-argument", upstreamErrorCodeForAccount([]byte(
		`{"code":"invalid-argument","error":"This model does not support reasoning_effort value none."}`,
	), &Account{Platform: PlatformGrok}))
	require.Equal(t, "", extractUpstreamErrorCode([]byte(`{"error":{"message":"Invalid 'input': expected an array."}}`)))
}
