//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-64: in an Otoha composite group one key calls Claude natively through POST /v1/messages (Anthropic Messages:
// tools, extended thinking, image input), and DeepSeek through its own Responses dialect on POST /v1/responses,
// passed through unchanged. Usage is billed at the routed provider's price times the group rate, and credit checks
// apply on /v1/messages as on /v1/responses.

const otohaNativeGroupID int64 = 6401

// otohaNativeUpstream answers like the provider and keeps what it was sent.
type otohaNativeUpstream struct {
	contentType string
	body        string
	path        string
	sent        []byte
	headers     http.Header
	calls       int
}

func (u *otohaNativeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.path = req.URL.Path
	u.headers = req.Header.Clone()
	if req.Body != nil {
		u.sent, _ = io.ReadAll(req.Body)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{u.contentType}, "Request-Id": []string{"req_upstream"}},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func (u *otohaNativeUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

// otohaNativeUsers is the key owner: their balance, and what billing takes from it.
type otohaNativeUsers struct {
	service.UserRepository
	balance  float64
	deducted []float64
}

func (u *otohaNativeUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id, Balance: u.balance, Status: service.StatusActive}, nil
}

func (u *otohaNativeUsers) DeductBalance(_ context.Context, _ int64, amount float64) error {
	u.deducted = append(u.deducted, amount)
	return nil
}

type otohaNativeUsageLogs struct {
	service.UsageLogRepository
	logs []*service.UsageLog
}

func (r *otohaNativeUsageLogs) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.logs = append(r.logs, log)
	return true, nil
}

// otohaNativeChannels prices each model under the provider that serves it, with a wrong price under another provider.
func otohaNativeChannels() *service.ChannelService {
	price := func(v float64) *float64 { return &v }
	return service.NewChannelService(&openAIWSUsageHandlerChannelRepoStub{
		channels: []service.Channel{{
			ID: 6402, Name: "otoha", Status: service.StatusActive, GroupIDs: []int64{otohaNativeGroupID},
			ModelPricing: []service.ChannelModelPricing{
				{Platform: service.PlatformAnthropic, Models: []string{"claude-sonnet-4-5"}, InputPrice: price(2e-6), OutputPrice: price(10e-6)},
				{Platform: service.PlatformAnthropic, Models: []string{"deepseek-v4-flash"}, InputPrice: price(100e-6), OutputPrice: price(100e-6)},
				{Platform: service.PlatformDeepSeek, Models: []string{"deepseek-v4-flash"}, InputPrice: price(0.2e-6), OutputPrice: price(0.8e-6)},
			},
		}},
		groupPlatforms: map[int64]string{otohaNativeGroupID: service.PlatformComposite},
	}, nil, nil, nil, nil)
}

func otohaNativeConfig() *config.Config {
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1
	return cfg
}

func otohaNativeGroup(balanceFallback bool) *service.Group {
	group := &service.Group{ID: otohaNativeGroupID, Hydrated: true, Platform: service.PlatformComposite, Status: service.StatusActive, RateMultiplier: 1.5}
	if balanceFallback {
		group.SubscriptionType = service.SubscriptionTypeSubscription
		group.BalanceFallbackEnabled = true
	}
	return group
}

func newOtohaNativeMessagesHandler(t *testing.T, group *service.Group, users *otohaNativeUsers, upstream *otohaNativeUpstream) (*GatewayHandler, *otohaNativeUsageLogs) {
	t.Helper()
	account := &service.Account{
		ID: 6403, Name: "claude", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-ant-upstream", "base_url": "http://anthropic.otoha.test"},
		Concurrency: 1, Priority: 1, Status: service.StatusActive, Schedulable: true,
		AccountGroups: []service.AccountGroup{{AccountID: 6403, GroupID: otohaNativeGroupID}},
	}
	cfg := otohaNativeConfig()
	logs := &otohaNativeUsageLogs{}
	billing := service.NewBillingService(cfg, nil)
	channels := otohaNativeChannels()
	billingCache := service.NewBillingCacheService(nil, users, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, logs, nil, users, nil, nil, nil, cfg,
		service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil),
		nil, billing, nil, billingCache, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil,
		channels, service.NewModelPricingResolver(channels, billing), nil, nil, nil,
	)
	return &GatewayHandler{
		gatewayService:           gateway,
		billingCacheService:      billingCache,
		concurrencyHelper:        NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
		cfg:                      cfg,
		maxAccountSwitches:       1,
		maxAccountSwitchesGemini: 1,
	}, logs
}

// otohaNativeContext is a request the way the gateway's middleware hands it on: the key authenticated, and the
// composite group's route for the model resolved on the request's endpoint.
func otohaNativeContext(path, body string, group *service.Group, routedTo string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-otoha-key")
	req.Header.Set("anthropic-version", "2023-06-01")
	ctx := context.WithValue(req.Context(), ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.UserID, int64(6404))
	model := gjson.Get(body, "model").String()
	endpoint := service.CompositeRouteEndpointResponses
	if strings.Contains(path, "/messages") {
		endpoint = service.CompositeRouteEndpointMessages
	}
	ctx = service.WithCompositeRouteDecision(ctx, service.CompositeRouteDecision{
		Matched: true, Source: service.CompositeRouteSourceDetector, GroupID: group.ID, PublicModel: model,
		TargetPlatform: routedTo, UpstreamModel: model, Endpoint: endpoint,
	})
	c.Request = req.WithContext(ctx)
	groupID := group.ID
	apiKey := &service.APIKey{
		ID: 6405, UserID: 6404, GroupID: &groupID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 6404, Concurrency: 10, Status: service.StatusActive},
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 6404, Concurrency: 10})
	return c, rec
}

const otohaClaudeToolsThinkingImageRequest = `{
	"model": "claude-sonnet-4-5",
	"max_tokens": 2048,
	"thinking": {"type": "enabled", "budget_tokens": 1024},
	"tools": [{"name": "get_weather", "description": "Weather for a city",
		"input_schema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}],
	"messages": [{"role": "user", "content": [
		{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}},
		{"type": "text", "text": "Which city is this? Get its weather."}
	]}]
}`

const otohaClaudeToolUseReply = `{
	"id": "msg_otoha_1", "type": "message", "role": "assistant", "model": "claude-sonnet-4-5",
	"content": [
		{"type": "thinking", "thinking": "The picture shows Tokyo Tower.", "signature": "sig-otoha"},
		{"type": "tool_use", "id": "toolu_otoha_1", "name": "get_weather", "input": {"city": "Tokyo"}}
	],
	"stop_reason": "tool_use", "stop_sequence": null,
	"usage": {"input_tokens": 1000, "output_tokens": 500}
}`

func TestOtohaCompositeKeyCallsClaudeNativelyOnMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &otohaNativeUpstream{contentType: "application/json", body: otohaClaudeToolUseReply}
	users := &otohaNativeUsers{balance: 10}
	group := otohaNativeGroup(false)
	h, logs := newOtohaNativeMessagesHandler(t, group, users, upstream)

	c, rec := otohaNativeContext("/v1/messages", otohaClaudeToolsThinkingImageRequest, group, service.PlatformAnthropic)
	h.Messages(c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, "/v1/messages", upstream.path, "forwarded to the Anthropic account's own Messages endpoint")
	require.Equal(t, "sk-ant-upstream", upstream.headers.Get("x-api-key"), "the account's key goes upstream, not the user's")
	sent := gjson.ParseBytes(upstream.sent)
	require.Equal(t, "claude-sonnet-4-5", sent.Get("model").String())
	require.Equal(t, "get_weather", sent.Get("tools.0.name").String(), "tools pass through in Anthropic form")
	require.Equal(t, "object", sent.Get("tools.0.input_schema.type").String())
	require.Equal(t, "enabled", sent.Get("thinking.type").String(), "extended thinking passes through")
	require.Equal(t, int64(1024), sent.Get("thinking.budget_tokens").Int())
	require.Equal(t, "image", sent.Get("messages.0.content.0.type").String(), "the image block passes through")
	require.Equal(t, "image/png", sent.Get("messages.0.content.0.source.media_type").String())

	reply := gjson.Parse(rec.Body.String())
	require.Equal(t, "thinking", reply.Get("content.0.type").String(), "the app gets Anthropic Messages back")
	require.Equal(t, "sig-otoha", reply.Get("content.0.signature").String(), "the thinking signature survives for the next turn")
	require.Equal(t, "tool_use", reply.Get("content.1.type").String())
	require.Equal(t, "Tokyo", reply.Get("content.1.input.city").String())
	require.Equal(t, "tool_use", reply.Get("stop_reason").String())

	require.Len(t, logs.logs, 1)
	want := (1000*2e-6 + 500*10e-6) * 1.5
	require.InDelta(t, want, logs.logs[0].ActualCost, 1e-12, "the anthropic channel price times the group rate")
	require.Equal(t, []float64{logs.logs[0].ActualCost}, users.deducted, "the balance pays the same amount")
}

func TestOtohaCompositeMessagesRefusesWhenPlanAndBalanceAreUsedUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &otohaNativeUpstream{contentType: "application/json", body: otohaClaudeToolUseReply}
	group := otohaNativeGroup(true)
	h, logs := newOtohaNativeMessagesHandler(t, group, &otohaNativeUsers{balance: 0}, upstream)

	c, rec := otohaNativeContext("/v1/messages", otohaClaudeToolsThinkingImageRequest, group, service.PlatformAnthropic)
	h.Messages(c)

	require.Equal(t, http.StatusPaymentRequired, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "CREDIT_EXHAUSTED")
	require.Zero(t, upstream.calls, "nothing reaches the provider")
	require.Empty(t, logs.logs)
}

// DeepSeek's own Responses dialect: reasoning items carry their text in content and have no summary array; the
// gateway passes the stream through as DeepSeek sent it.
const otohaDeepSeekNativeStream = "event: response.created\n" +
	"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_ds_1\",\"status\":\"in_progress\",\"model\":\"deepseek-v4-flash\"}}\n\n" +
	"event: response.output_item.done\n" +
	"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_ds_1\",\"type\":\"reasoning\",\"content\":[{\"type\":\"reasoning_text\",\"text\":\"Think first.\"}]}}\n\n" +
	"event: response.output_item.done\n" +
	"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"id\":\"msg_ds_1\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello.\"}]}}\n\n" +
	"event: response.completed\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ds_1\",\"status\":\"completed\",\"model\":\"deepseek-v4-flash\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":500}}}\n\n"

func TestOtohaCompositeKeyCallsDeepSeekNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := otohaNativeGroup(false)
	account := service.Account{
		ID: 6406, Name: "deepseek", Platform: service.PlatformDeepSeek, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{otohaNativeGroupID},
		Credentials: map[string]any{"api_key": "sk-deepseek-upstream", "base_url": "http://deepseek.otoha.test"},
		Extra:       map[string]any{service.DeepSeekUserIsolationModeKey: service.DeepSeekUserIsolationModeOff},
	}
	cfg := otohaNativeConfig()
	users := &otohaNativeUsers{balance: 10}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
	billingCache := service.NewBillingCacheService(nil, users, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	channels := otohaNativeChannels()
	upstream := &otohaNativeUpstream{contentType: "text/event-stream", body: otohaDeepSeekNativeStream}
	gateway := service.NewOpenAIGatewayService(
		&openAIWSUsageHandlerAccountRepoStub{account: account}, usage, nil, users, nil, nil, nil, cfg, nil,
		service.NewConcurrencyService(nil), billing, nil, billingCache, upstream, &service.DeferredService{},
		nil, nil, service.NewModelPricingResolver(channels, billing), channels, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)

	c, rec := otohaNativeContext("/v1/responses",
		`{"model":"deepseek-v4-flash","input":"Say hello.","reasoning":{"effort":"high"},"stream":true}`, group, service.PlatformDeepSeek)
	c.Request.Header.Del("x-api-key")
	c.Request.Header.Set("Authorization", "Bearer sk-otoha-key")
	h.Responses(c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "/responses", upstream.path, "DeepSeek's own Responses endpoint")
	require.Equal(t, "Bearer sk-deepseek-upstream", upstream.headers.Get("Authorization"))
	require.Equal(t, "deepseek-v4-flash", gjson.GetBytes(upstream.sent, "model").String())
	body := rec.Body.String()
	require.Contains(t, body, `"type":"reasoning","content":[{"type":"reasoning_text","text":"Think first."}]`,
		"the reasoning item reaches the app as DeepSeek wrote it")
	require.NotContains(t, body, `"summary"`, "no OpenAI summary array is made up")
	require.Contains(t, body, `"text":"Hello."`)

	select {
	case log := <-usage.created:
		want := (1000*0.2e-6 + 500*0.8e-6) * 1.5
		require.InDelta(t, want, log.ActualCost, 1e-12, "the deepseek channel price times the group rate, not the anthropic row")
	case <-time.After(2 * time.Second):
		t.Fatal("the DeepSeek request was not billed")
	}
}
