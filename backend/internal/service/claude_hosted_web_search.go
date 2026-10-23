package service

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// hostedWebSearchUnsupportedMessage is shown to the caller. The Go error stays
// uncapitalized so it can be wrapped; the sentence is what the client reads.
const hostedWebSearchUnsupportedMessage = "This account cannot run hosted web search. Remove the web_search tool, or use an Anthropic API key, a Claude subscription, DeepSeek messages, or an Anthropic-compatible passthrough"

var errHostedWebSearchUnsupported = errors.New("hosted web search is not available on this account")

// messagesBodyHasHostedWebSearch reports whether the Messages body declares
// Anthropic's server-side web search tool. A client function that happens to
// be named web_search is not hosted search.
func messagesBodyHasHostedWebSearch(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, tool gjson.Result) bool {
		if isHostedWebSearchTool(tool) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isHostedWebSearchTool(tool gjson.Result) bool {
	toolType := strings.TrimSpace(tool.Get("type").String())
	switch toolType {
	case "", "function", "custom":
		return false
	}
	return strings.HasPrefix(toolType, "web_search") || toolType == "google_search"
}

// accountForwardsHostedWebSearch reports whether the upstream receives the
// Anthropic web_search tool unchanged, or translates it and still returns
// server_tool_use blocks. Routes that would drop the tool return false.
func accountForwardsHostedWebSearch(account *Account, upstreamModel, protocol string) bool {
	if account == nil {
		return false
	}
	switch account.Platform {
	case PlatformAnthropic:
		// API key, Claude subscription, Bedrock and Vertex keep the Messages
		// tool in the body. An upstream that rejects it answers with its own error.
		return true
	case PlatformDeepSeek:
		return account.IsDeepSeekAPIKey()
	case PlatformAntigravity:
		// OAuth is rewritten to Gemini and drops or flattens the search.
		// An upstream passthrough posts the original Messages body.
		return account.Type == AccountTypeUpstream
	case PlatformOpenAI:
		switch protocol {
		case APIProtocolResponses:
			return true
		case APIProtocolAnthropic:
			// GLM, Kimi and the other passback-required endpoints reject
			// server_tool_use. Claude-shaped native endpoints keep the tool.
			return ResolveThinkingProtocol(upstreamModel) != ThinkingProtocolPassbackRequired
		default:
			return false
		}
	default:
		return false
	}
}

// hostedWebSearchRefusal is nil when the body has no hosted search tool, or
// when this account will forward it. Otherwise it is the refusal to send.
func hostedWebSearchRefusal(account *Account, upstreamModel, protocol string, body []byte) error {
	if !messagesBodyHasHostedWebSearch(body) {
		return nil
	}
	if accountForwardsHostedWebSearch(account, upstreamModel, protocol) {
		return nil
	}
	return errHostedWebSearchUnsupported
}

// noteClaudeWebSearchRequests copies usage.server_tool_use.web_search_requests
// onto the usage record. A missing or zero count leaves the previous value.
func noteClaudeWebSearchRequests(usage *ClaudeUsage, node gjson.Result) {
	if usage == nil || !node.Exists() {
		return
	}
	count := node.Get("server_tool_use.web_search_requests")
	if count.Exists() && count.Int() > 0 {
		usage.WebSearchRequests = int(count.Int())
	}
}

// applyClaudeHostedSearchCount bills the upstream search count on top of tokens.
func applyClaudeHostedSearchCount(result *ForwardResult) {
	if result == nil || result.Usage.WebSearchRequests <= 0 {
		return
	}
	result.SearchCount = result.Usage.WebSearchRequests
}
