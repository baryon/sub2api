//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessagesBodyHasHostedWebSearch(t *testing.T) {
	hosted := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`)
	require.True(t, messagesBodyHasHostedWebSearch(hosted))

	mixed := []byte(`{"tools":[{"name":"bash","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}]}`)
	require.True(t, messagesBodyHasHostedWebSearch(mixed))

	clientFunction := []byte(`{"tools":[{"name":"web_search","description":"client search","input_schema":{"type":"object"}}]}`)
	require.False(t, messagesBodyHasHostedWebSearch(clientFunction))

	custom := []byte(`{"tools":[{"type":"custom","name":"web_search"}]}`)
	require.False(t, messagesBodyHasHostedWebSearch(custom))
}

func TestHostedWebSearchRefusalByAccount(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5-5-low","tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],"messages":[{"role":"user","content":"search"}]}`)
	cases := []struct {
		name     string
		account  *Account
		model    string
		protocol string
		refuse   bool
	}{
		{"anthropic api key forwards", &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, "claude-sonnet-5-5", "", false},
		{"anthropic oauth forwards", &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, "claude-sonnet-5-5", "", false},
		{"setup token forwards", &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}, "claude-sonnet-4-6", "", false},
		{"bedrock forwards the tool", &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}, "claude-sonnet-4-6", "", false},
		{"deepseek messages forwards", &Account{Platform: PlatformDeepSeek, Type: AccountTypeAPIKey}, "deepseek-v4-pro", "", false},
		{"antigravity upstream passthrough forwards", &Account{Platform: PlatformAntigravity, Type: AccountTypeUpstream}, "claude-sonnet-5-5", "", false},
		{"antigravity oauth refuses", &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth}, "claude-sonnet-5-5", "", true},
		{"gemini refuses", &Account{Platform: PlatformGemini, Type: AccountTypeAPIKey}, "gemini-2.5-pro", "", true},
		{"openai chat completions refuses", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-4o", APIProtocolChatCompletions, true},
		{"openai responses translates", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-6.1-sol", APIProtocolResponses, false},
		{"native claude messages forwards", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "claude-sonnet-4-6", APIProtocolAnthropic, false},
		{"passback anthropic endpoint refuses", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "glm-4.6", APIProtocolAnthropic, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := hostedWebSearchRefusal(tc.account, tc.model, tc.protocol, body)
			if tc.refuse {
				require.Error(t, err)
				require.Contains(t, err.Error(), "hosted web search")
				return
			}
			require.NoError(t, err)
		})
	}

	plain := []byte(`{"tools":[{"name":"bash","input_schema":{"type":"object"}}]}`)
	require.NoError(t, hostedWebSearchRefusal(&Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth}, "claude-sonnet-5-5", "", plain))
}

func TestClaudeUsageRecordsHostedWebSearchRequests(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":3,"output_tokens":4,"server_tool_use":{"web_search_requests":2}}}`)
	usage := parseClaudeUsageFromResponseBody(body)
	require.Equal(t, 2, usage.WebSearchRequests)

	missing := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
	require.Equal(t, 0, missing.WebSearchRequests)

	parseSSEUsagePassthrough(`{"type":"message_delta","usage":{"output_tokens":8,"server_tool_use":{"web_search_requests":1}}}`, usage)
	require.Equal(t, 1, usage.WebSearchRequests)
	require.Equal(t, 8, usage.OutputTokens)

	var result ForwardResult
	result.Usage = *usage
	applyClaudeHostedSearchCount(&result)
	require.Equal(t, 1, result.SearchCount)
	require.True(t, strings.Contains(hostedWebSearchUnsupportedMessage, "web_search"))
}
