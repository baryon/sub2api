//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-87: the Otoha app speaks Google's generateContent protocol to the gateway with an Otoha key. These tests run the
// real /v1beta handlers for a composite group (the Otoha group) with Gemini accounts behind it, and a scripted Google.

const (
	compositeGeminiGroupID = int64(2)
	compositeGeminiKeyID   = int64(9302)
	compositeGeminiUserID  = int64(9303)
)

// googleCall is one request the gateway made to Google.
type googleCall struct {
	method  string
	url     string
	apiKey  string
	bearer  string
	body    []byte
	account int64
}

// scriptedGoogle answers the gateway's calls to Google, per URL path suffix.
type scriptedGoogle struct {
	service.HTTPUpstream
	mu      sync.Mutex
	calls   []googleCall
	respond func(call googleCall) (int, string, string) // status, content type, body
}

func (g *scriptedGoogle) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	call := googleCall{
		method: req.Method, url: req.URL.String(), apiKey: req.Header.Get("x-goog-api-key"),
		bearer: req.Header.Get("Authorization"), body: body, account: accountID,
	}
	g.mu.Lock()
	g.calls = append(g.calls, call)
	respond := g.respond
	g.mu.Unlock()
	status, contentType, reply := http.StatusOK, "application/json", `{}`
	if respond != nil {
		status, contentType, reply = respond(call)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {contentType}, "X-Request-Id": {"google-request-1"}},
		Body:       io.NopCloser(strings.NewReader(reply)),
	}, nil
}

func (g *scriptedGoogle) recorded() []googleCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]googleCall(nil), g.calls...)
}

// compositeGeminiSchedulerCache serves the accounts of the bucket's platform, the way the real snapshot does.
type compositeGeminiSchedulerCache struct {
	fakeSchedulerCache
}

func (f *compositeGeminiSchedulerCache) GetSnapshot(_ context.Context, bucket service.SchedulerBucket) ([]*service.Account, bool, error) {
	out := make([]*service.Account, 0, len(f.accounts))
	for _, account := range f.accounts {
		if account.Platform == bucket.Platform || (bucket.Mode == service.SchedulerModeMixed && account.Platform == service.PlatformAntigravity) {
			out = append(out, account)
		}
	}
	return out, true, nil
}

type compositeGeminiRouteRepo struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
}

func (r compositeGeminiRouteRepo) ListByGroup(context.Context, int64, bool) ([]service.CompositeModelRoute, error) {
	return r.routes, nil
}

// compositeGeminiUsageRepo keeps the usage the gateway wrote.
type compositeGeminiUsageRepo struct {
	service.UsageLogRepository
	mu   sync.Mutex
	logs []*service.UsageLog
}

func (r *compositeGeminiUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return true, nil
}

type compositeGeminiFixture struct {
	handler  *GatewayHandler
	group    *service.Group
	apiKey   *service.APIKey
	google   *scriptedGoogle
	accounts []*service.Account
	usage    *compositeGeminiUsageRepo
	resolver *service.CompositeRouteResolver
	caches   *geminiCachedContentHandlerRepoStub
	cleanup  func()
}

func compositeGeminiAccount(id int64, priority int, mutate func(*service.Account)) *service.Account {
	account := &service.Account{
		ID:            id,
		Name:          "gemini",
		Platform:      service.PlatformGemini,
		Type:          service.AccountTypeAPIKey,
		Credentials:   map[string]any{"api_key": "google-key-" + string(rune('a'+id%26))},
		Status:        service.StatusActive,
		Schedulable:   true,
		Concurrency:   2,
		Priority:      priority,
		AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: compositeGeminiGroupID}},
	}
	if mutate != nil {
		mutate(account)
	}
	return account
}

func newCompositeGeminiFixture(t *testing.T, routes []service.CompositeModelRoute, accounts ...*service.Account) *compositeGeminiFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &compositeGeminiFixture{
		accounts: accounts,
		google:   &scriptedGoogle{},
		usage:    &compositeGeminiUsageRepo{},
		caches:   &geminiCachedContentHandlerRepoStub{},
		group: &service.Group{
			ID: compositeGeminiGroupID, Hydrated: true, Platform: service.PlatformComposite, Status: service.StatusActive, RateMultiplier: 1,
		},
	}
	schedulerCache := &compositeGeminiSchedulerCache{fakeSchedulerCache: fakeSchedulerCache{accounts: accounts}}
	snapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, nil, nil, nil)
	resolver := service.NewCompositeRouteResolver(compositeGeminiRouteRepo{routes: routes})
	f.resolver = resolver

	cfg := &config.Config{RunMode: config.RunModeSimple}
	billing := service.NewBillingService(cfg, nil)
	gateway := service.NewGatewayService(
		nil, &fakeGroupRepo{group: f.group}, f.usage, nil, nil, nil, nil, nil, cfg, snapshot, nil, billing, nil,
		service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil), nil, nil, &service.DeferredService{},
		nil, nil, nil, service.NewDigestSessionStore(), nil, nil, nil, nil, resolver, nil, nil,
	)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	f.cleanup = billingCache.Stop
	t.Cleanup(f.cleanup)

	f.handler = &GatewayHandler{
		gatewayService:             gateway,
		billingCacheService:        billingCache,
		concurrencyHelper:          NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
		geminiCachedContentService: service.NewGeminiCachedContentService(f.caches, nil, &config.Config{}),
		geminiCompatService:        service.NewGeminiMessagesCompatService(nil, &fakeGroupRepo{group: f.group}, nil, snapshot, nil, nil, f.google, nil, &config.Config{}),
		maxAccountSwitches:         1,
		maxAccountSwitchesGemini:   1,
	}
	f.apiKey = &service.APIKey{
		ID: compositeGeminiKeyID, UserID: compositeGeminiUserID, GroupID: &f.group.ID, Group: f.group, Status: service.StatusActive,
		User: &service.User{ID: compositeGeminiUserID, Concurrency: 10, Balance: 100},
	}
	return f
}

// call sends a /v1beta request the way the chain in front of the handler does for a composite group: the key is
// authenticated, and the model is resolved for the Gemini endpoint.
func (f *compositeGeminiFixture) call(t *testing.T, method, path, body string, handler gin.HandlerFunc, route string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.Handle(method, route, func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), f.apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: f.apiKey.UserID, Concurrency: 10})
		model := compositeGeminiModelOf(c)
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, ctxkey.Group, f.group)
		if model != "" {
			decision, err := f.resolver.Resolve(ctx, f.group.ID, model, service.CompositeRouteEndpointGemini)
			require.NoError(t, err)
			if decision.Matched {
				ctx = service.WithCompositeRouteDecision(ctx, decision)
			}
		}
		if _, ok := service.ResolvedTargetPlatformFromContext(ctx); !ok {
			ctx = service.WithResolvedTargetPlatform(ctx, service.PlatformGemini)
		}
		c.Request = c.Request.WithContext(ctx)
		handler(c)
	})
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func compositeGeminiModelOf(c *gin.Context) string {
	if model := strings.TrimSpace(c.Param("model")); model != "" {
		return model
	}
	action := strings.TrimPrefix(strings.TrimSpace(c.Param("modelAction")), "/")
	if idx := strings.LastIndex(action, ":"); idx >= 0 {
		return action[:idx]
	}
	return action
}

// seedCache is a cached content the key made on an account: the client knows its public name, Google the upstream one.
func (f *compositeGeminiFixture) seedCache(t *testing.T, publicID string, accountID int64, model string) {
	t.Helper()
	require.NoError(t, f.caches.Create(context.Background(), &service.GeminiCachedContent{
		PublicID: publicID, UserID: compositeGeminiUserID, APIKeyID: compositeGeminiKeyID, GroupID: compositeGeminiGroupID,
		AccountID: accountID, UpstreamName: "cachedContents/up-" + publicID, Model: model, UpstreamModel: "models/" + model,
		TotalTokenCount: 4096, ExpireTime: time.Now().Add(time.Hour),
	}))
}

func (f *compositeGeminiFixture) generate(t *testing.T, model, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodPost, "/v1beta/models/"+model+":"+action, body, f.handler.GeminiV1BetaModels, "/v1beta/models/*modelAction")
}

const geminiTwoTurnRequest = `{"contents":[{"role":"user","parts":[{"text":"what is the weather in Paris"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"low","includeThoughts":true}}}`

// ---- non-streaming ----

// A native generateContent call through the composite group reaches the Gemini account with its own key, and Google's
// reply, including the thought part, its signature and the usage, arrives as Google wrote it.
func TestCompositeGeminiGenerateContentPassesThroughUnchanged(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	reply := `{"candidates":[{"content":{"role":"model","parts":[{"text":"I should look it up","thought":true},{"functionCall":{"name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"CiQBsig123=="}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":15,"totalTokenCount":180,"thoughtsTokenCount":45,"cachedContentTokenCount":100},"modelVersion":"gemini-3.5-flash","responseId":"abc"}`
	f.google.respond = func(googleCall) (int, string, string) { return 200, "application/json; charset=UTF-8", reply }

	rec := f.generate(t, "gemini-3.5-flash", "generateContent", geminiTwoTurnRequest)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, reply, rec.Body.String())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, int64(12), calls[0].account)
	require.Contains(t, calls[0].url, "/v1beta/models/gemini-3.5-flash:generateContent")
	require.NotContains(t, calls[0].url, "alt=sse")
	require.Equal(t, "google-key-"+string(rune('a'+12%26)), calls[0].apiKey, "the account's own key, not the Otoha key")
	require.Empty(t, calls[0].bearer)
	require.JSONEq(t, geminiTwoTurnRequest, string(calls[0].body), "the request is forwarded as written")
}

// The second turn of a tool call carries the signature back; it reaches Google untouched, and the thought parts of
// the history too.
func TestCompositeGeminiSecondTurnKeepsThoughtSignatures(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	f.seedCache(t, "otoha1234", 12, "gemini-3.5-flash")
	f.google.respond = func(googleCall) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"content":{"role":"model","parts":[{"text":"It is sunny."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":200,"candidatesTokenCount":5}}`
	}
	second := `{"contents":[{"role":"user","parts":[{"text":"what is the weather in Paris"}]},` +
		`{"role":"model","parts":[{"text":"I should look it up","thought":true,"thoughtSignature":"thought-sig-1"},{"functionCall":{"name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"CiQBsig123=="},{"functionCall":{"name":"get_time","args":{}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"temp":21}}},{"functionResponse":{"name":"get_time","response":{"t":"noon"}}}]}],` +
		`"cachedContent":"cachedContents/otoha1234"}`

	rec := f.generate(t, "gemini-3.5-flash", "generateContent", second)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	sent := f.google.recorded()[0].body
	require.Equal(t, "thought-sig-1", gjson.GetBytes(sent, "contents.1.parts.0.thoughtSignature").String())
	require.Equal(t, "CiQBsig123==", gjson.GetBytes(sent, "contents.1.parts.1.thoughtSignature").String())
	require.True(t, gjson.GetBytes(sent, "contents.1.parts.0.thought").Bool())
	require.Equal(t, "get_time", gjson.GetBytes(sent, "contents.1.parts.2.functionCall.name").String())
	require.Equal(t, "cachedContents/up-otoha1234", gjson.GetBytes(sent, "cachedContent").String(), "the cache is named as Google knows it")
}

// The two turns of a tool call, with no cache involved: the first answer carries the thought and its signature, the
// second request returns them, and Google gets them as it sent them. The gateway recognises the conversation by its
// start, so the signature is not replaced as it is when a conversation moves to another account.
func TestCompositeGeminiToolCallOverTwoTurnsKeepsTheSignatures(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	first := `{"candidates":[{"content":{"role":"model","parts":[{"text":"I should look it up","thought":true,"thoughtSignature":"thought-sig-1"},{"functionCall":{"name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"CiQBsig123=="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":15,"thoughtsTokenCount":45}}`
	second := `{"candidates":[{"content":{"role":"model","parts":[{"text":"It is sunny."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":200,"candidatesTokenCount":5}}`
	reply := first
	f.google.respond = func(googleCall) (int, string, string) { return 200, "application/json", reply }

	turn1 := f.generate(t, "gemini-3.5-flash", "generateContent", geminiTwoTurnRequest)
	require.Equal(t, http.StatusOK, turn1.Code, turn1.Body.String())
	require.JSONEq(t, first, turn1.Body.String())

	reply = second
	turn2Request := `{"contents":[{"role":"user","parts":[{"text":"what is the weather in Paris"}]},` +
		`{"role":"model","parts":[{"text":"I should look it up","thought":true,"thoughtSignature":"thought-sig-1"},{"functionCall":{"name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"CiQBsig123=="}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"temp":21}}}]}],` +
		`"generationConfig":{"thinkingConfig":{"thinkingLevel":"low","includeThoughts":true}}}`
	turn2 := f.generate(t, "gemini-3.5-flash", "generateContent", turn2Request)
	require.Equal(t, http.StatusOK, turn2.Code, turn2.Body.String())
	require.JSONEq(t, second, turn2.Body.String())

	calls := f.google.recorded()
	require.Len(t, calls, 2)
	require.JSONEq(t, turn2Request, string(calls[1].body), "the second turn reaches Google as the app wrote it")
}

// ---- streaming ----

// A streaming call goes to Google's SSE endpoint, and each event comes back as it was sent.
func TestCompositeGeminiStreamGenerateContentPassesTheEventsThrough(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me check. ","thought":true}]}}],"usageMetadata":{"promptTokenCount":120,"totalTokenCount":120},"modelVersion":"gemini-3.5-flash"}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"CiQBsig123=="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":15,"totalTokenCount":180,"thoughtsTokenCount":45,"cachedContentTokenCount":100},"modelVersion":"gemini-3.5-flash"}`,
	}
	stream := "data: " + events[0] + "\r\n\r\ndata: " + events[1] + "\r\n\r\n"
	f.google.respond = func(googleCall) (int, string, string) { return 200, "text/event-stream", stream }

	rec := f.generate(t, "gemini-3.5-flash", "streamGenerateContent?alt=sse", geminiTwoTurnRequest)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	var got []string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			got = append(got, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	require.Len(t, got, 2)
	require.JSONEq(t, events[0], got[0])
	require.JSONEq(t, events[1], got[1])
	require.Equal(t, "CiQBsig123==", gjson.Get(got[1], "candidates.0.content.parts.0.thoughtSignature").String())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Contains(t, calls[0].url, ":streamGenerateContent")
	require.Contains(t, calls[0].url, "alt=sse")
}

// ---- usage ----

// What Google reports is what is recorded for the key: the prompt without the cached part, the cached part, and the
// answer with the thoughts — the same counts the Messages path records (TASK-86).
func TestCompositeGeminiCallsAreRecordedWithGooglesCounts(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	usageMetadata := `"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":15,"totalTokenCount":180,"thoughtsTokenCount":45,"cachedContentTokenCount":100}`
	f.google.respond = func(call googleCall) (int, string, string) {
		if strings.Contains(call.url, "streamGenerateContent") {
			return 200, "text/event-stream", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]," + usageMetadata + "}\n\n"
		}
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],` + usageMetadata + `}`
	}

	for _, action := range []string{"generateContent", "streamGenerateContent?alt=sse"} {
		f.usage.logs = nil
		rec := f.generate(t, "gemini-3.5-flash", action, geminiTwoTurnRequest)
		require.Equal(t, http.StatusOK, rec.Code, action)
		require.Len(t, f.usage.logs, 1, action)
		log := f.usage.logs[0]
		require.Equal(t, "gemini-3.5-flash", log.Model, action)
		require.Equal(t, 20, log.InputTokens, action)
		require.Equal(t, 100, log.CacheReadTokens, action)
		require.Equal(t, 60, log.OutputTokens, action)
		require.Equal(t, compositeGeminiKeyID, log.APIKeyID, action)
		require.Equal(t, int64(12), log.AccountID, action)
	}
}

// ---- model routing ----

// The group's Gemini accounts serve a Gemini model; an account of another provider never gets the call.
func TestCompositeGeminiRoutesToTheGeminiAccountOnly(t *testing.T) {
	claude := &service.Account{
		ID: 13, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "anthropic-key"},
		Status: service.StatusActive, Schedulable: true, Concurrency: 2, Priority: 0,
		AccountGroups: []service.AccountGroup{{AccountID: 13, GroupID: compositeGeminiGroupID}},
	}
	f := newCompositeGeminiFixture(t, nil, claude, compositeGeminiAccount(12, 5, nil))
	f.google.respond = func(googleCall) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	}

	rec := f.generate(t, "gemini-3.5-flash", "generateContent", geminiTwoTurnRequest)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, int64(12), calls[0].account, "account 13 is an Anthropic account with a better priority and is not asked")
}

// A model alias of the group, routed to a Gemini model, is forwarded under the upstream name.
func TestCompositeGeminiAliasIsForwardedAsTheUpstreamModel(t *testing.T) {
	routes := []service.CompositeModelRoute{{
		ID: 1, GroupID: compositeGeminiGroupID, PublicModel: "otoha-fast", MatchType: service.CompositeRouteMatchExact,
		TargetPlatform: service.PlatformGemini, UpstreamModel: "gemini-3.5-flash", Endpoint: service.CompositeRouteEndpointGemini,
		Priority: 100, Enabled: true,
	}}
	f := newCompositeGeminiFixture(t, routes, compositeGeminiAccount(12, 1, nil))
	f.google.respond = func(googleCall) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	}

	rec := f.generate(t, "otoha-fast", "generateContent", geminiTwoTurnRequest)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Contains(t, calls[0].url, "/models/gemini-3.5-flash:generateContent")
}

// ---- per-model cooldown (TASK-85) ----

func cooledFor(model string) func(*service.Account) {
	return func(a *service.Account) {
		a.Extra = map[string]any{"model_rate_limits": map[string]any{
			model: map[string]any{"rate_limit_reset_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)},
		}}
	}
}

// One model cooling down on the account does not stop the others.
func TestCompositeGeminiCooledModelDoesNotBlockTheOthers(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, cooledFor("gemini-3.1-pro-preview")))
	f.google.respond = func(googleCall) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	}

	flash := f.generate(t, "gemini-3.5-flash", "generateContent", geminiTwoTurnRequest)
	require.Equal(t, http.StatusOK, flash.Code, flash.Body.String())

	pro := f.generate(t, "gemini-3.1-pro-preview", "generateContent", geminiTwoTurnRequest)
	// the only account is cooling down for this model
	require.Len(t, f.google.recorded(), 1, "Google was not asked for the cooled model")
	require.Equal(t, http.StatusServiceUnavailable, pro.Code)
	require.Contains(t, pro.Body.String(), "model_rate_limited=1")
}

// With a second account, the cooled model goes there.
func TestCompositeGeminiCooledModelGoesToAnotherAccount(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil,
		compositeGeminiAccount(12, 1, cooledFor("gemini-3.1-pro-preview")),
		compositeGeminiAccount(14, 5, nil),
	)
	f.google.respond = func(googleCall) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	}

	pro := f.generate(t, "gemini-3.1-pro-preview", "generateContent", geminiTwoTurnRequest)

	require.Equal(t, http.StatusOK, pro.Code, pro.Body.String())
	require.Equal(t, int64(14), f.google.recorded()[0].account)
}

// ---- cached contents ----

// A request that refers to a cache the key made names it to Google as Google knows it, goes to the account that holds
// it, and Google's usage, with the cached tokens, comes back as it was.
func TestCompositeGeminiCachedContentReferenceGoesToTheAccountHoldingIt(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil), compositeGeminiAccount(14, 1, nil))
	f.seedCache(t, "abc123", 14, "gemini-3.5-flash")
	f.google.respond = func(googleCall) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":500,"cachedContentTokenCount":480,"candidatesTokenCount":3}}`
	}
	body, err := json.Marshal(map[string]any{
		"contents":      []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "and tomorrow?"}}}},
		"cachedContent": "cachedContents/abc123",
	})
	require.NoError(t, err)

	rec := f.generate(t, "gemini-3.5-flash", "generateContent", string(body))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, int64(480), gjson.Get(rec.Body.String(), "usageMetadata.cachedContentTokenCount").Int())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, int64(14), calls[0].account)
	require.Equal(t, "cachedContents/up-abc123", gjson.GetBytes(calls[0].body, "cachedContent").String())
}

// A reference to a cache the key does not have is refused before Google is asked.
func TestCompositeGeminiUnknownCachedContentIsRefused(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))

	rec := f.generate(t, "gemini-3.5-flash", "generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"cachedContent":"cachedContents/nope"}`)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Empty(t, f.google.recorded())
}

// ---- model list ----

const googleModelList = `{"models":[` +
	`{"name":"models/gemini-3.5-flash","displayName":"Gemini 3.5 Flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent","countTokens"]},` +
	`{"name":"models/gemini-3.1-pro-preview","displayName":"Gemini 3.1 Pro","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]},` +
	`{"name":"models/gemma-3-27b-it","displayName":"Gemma 3 27B","supportedGenerationMethods":["generateContent"]},` +
	`{"name":"models/text-embedding-004","displayName":"Text Embedding 004","supportedGenerationMethods":["embedContent"]}` +
	`],"nextPageToken":"page2"}`

func (f *compositeGeminiFixture) list(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return f.listPage(t, "")
}

func (f *compositeGeminiFixture) listPage(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodGet, "/v1beta/models"+query, "", f.handler.GeminiV1BetaListModels, "/v1beta/models")
}

func compositeGeminiModelNames(t *testing.T, body string) []string {
	t.Helper()
	var names []string
	for _, item := range gjson.Get(body, "models").Array() {
		names = append(names, item.Get("name").String())
	}
	return names
}

// The list of a composite group holds the models its Gemini side serves, with Google's own details, and nothing of
// the group's other providers or of Google's models the group does not route.
func TestCompositeGeminiModelListHoldsOnlyTheGroupsGeminiModels(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil,
		compositeGeminiAccount(12, 1, nil),
		&service.Account{
			ID: 13, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "k"},
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, AccountGroups: []service.AccountGroup{{AccountID: 13, GroupID: compositeGeminiGroupID}},
		},
	)
	f.google.respond = func(googleCall) (int, string, string) { return 200, "application/json", googleModelList }

	rec := f.list(t)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"models/gemini-3.5-flash", "models/gemini-3.1-pro-preview"}, compositeGeminiModelNames(t, rec.Body.String()))
	require.Equal(t, int64(1048576), gjson.Get(rec.Body.String(), "models.0.inputTokenLimit").Int(), "Google's own details are kept")
	require.Equal(t, "page2", gjson.Get(rec.Body.String(), "nextPageToken").String())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, int64(12), calls[0].account)
}

// The allowlist narrows the list further.
func TestCompositeGeminiModelListFollowsTheAllowlist(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	f.group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-3.5-flash"}}
	f.google.respond = func(googleCall) (int, string, string) { return 200, "application/json", googleModelList }

	rec := f.list(t)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"models/gemini-3.5-flash"}, compositeGeminiModelNames(t, rec.Body.String()))
}

// An alias the group routes to Gemini is listed with the models it can be called by.
func TestCompositeGeminiModelListIncludesGeminiAliases(t *testing.T) {
	routes := []service.CompositeModelRoute{
		{ID: 1, GroupID: compositeGeminiGroupID, PublicModel: "otoha-fast", MatchType: service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformGemini, UpstreamModel: "gemini-3.5-flash", Endpoint: service.CompositeRouteEndpointGemini, Priority: 100, Enabled: true},
		{ID: 2, GroupID: compositeGeminiGroupID, PublicModel: "otoha-claude", MatchType: service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformAnthropic, UpstreamModel: "claude-sonnet-4-5", Endpoint: service.CompositeRouteEndpointAny, Priority: 100, Enabled: true},
	}
	f := newCompositeGeminiFixture(t, routes, compositeGeminiAccount(12, 1, nil))
	f.google.respond = func(googleCall) (int, string, string) { return 200, "application/json", googleModelList }

	rec := f.list(t)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	names := compositeGeminiModelNames(t, rec.Body.String())
	require.Contains(t, names, "models/otoha-fast")
	require.NotContains(t, names, "models/otoha-claude")
	require.NotContains(t, names, "models/gemma-3-27b-it")
}

// The app reads the list page by page (pageSize, pageToken): Google is asked for the page the app asked for, and only
// those two settings are passed on — never the key the app signed in with.
func TestCompositeGeminiModelListPassesThePageOn(t *testing.T) {
	f := newCompositeGeminiFixture(t, nil, compositeGeminiAccount(12, 1, nil))
	f.google.respond = func(googleCall) (int, string, string) { return 200, "application/json", googleModelList }

	rec := f.listPage(t, "?pageSize=2&pageToken=page2-token_1%3D&key=otoha-secret&extra=1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	calls := f.google.recorded()
	require.Len(t, calls, 1)
	require.Contains(t, calls[0].url, "pageSize=2")
	require.Contains(t, calls[0].url, "pageToken=page2-token_1%3D")
	require.NotContains(t, calls[0].url, "otoha-secret")
	require.NotContains(t, calls[0].url, "extra")
	require.NotContains(t, calls[0].url, "key=")

	for _, bad := range []string{"?pageSize=abc", "?pageSize=-1", "?pageSize=100000", "?pageToken=" + strings.Repeat("a", 3000), "?pageToken=a%20b%26key%3Dx"} {
		f.google.calls = nil
		rec = f.listPage(t, bad)
		require.Equal(t, http.StatusBadRequest, rec.Code, bad)
		require.Empty(t, f.google.recorded(), bad)
	}
}
