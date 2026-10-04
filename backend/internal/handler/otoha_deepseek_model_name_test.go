//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-65: the Otoha DeepSeek account maps the public name deepseek-v4-flash to DeepSeek's own name deepseek-flash.
// DeepSeek's native Responses answer names deepseek-flash, and the app (which asked for deepseek-v4-flash) rejects
// an answer for another model. The gateway hands the answer back under the name the client asked for, changing
// nothing else, and bills exactly as before.

// otohaDeepSeekFlashStream is shaped like a real DeepSeek Responses stream (trimmed, ids anonymised): the response
// object names the upstream model in created, in_progress and completed; reasoning items carry their text in
// content and an empty summary, as DeepSeek sends them. The answer text mentions the upstream name on purpose: only
// protocol model fields may change.
const otohaDeepSeekFlashStream = "event: response.created\n" +
	`data: {"type":"response.created","response":{"id":"resp_ds_65","object":"response","created_at":1791127231,"status":"in_progress","background":false,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"model":"deepseek-flash","output":[],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":"high","summary":null},"service_tier":"default","store":false,"temperature":1.0,"text":{"format":{"type":"text"},"verbosity":null},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"metadata":{}},"sequence_number":0}` + "\n\n" +
	"event: response.in_progress\n" +
	`data: {"type":"response.in_progress","response":{"id":"resp_ds_65","object":"response","created_at":1791127231,"status":"in_progress","background":false,"error":null,"model":"deepseek-flash","output":[],"reasoning":{"effort":"high","summary":null},"usage":null,"metadata":{}},"sequence_number":1}` + "\n\n" +
	"event: response.output_item.added\n" +
	`data: {"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_ds_65","status":"in_progress","content":[],"summary":[],"encrypted_content":"resp_ds_65-0"},"output_index":0,"sequence_number":2}` + "\n\n" +
	"event: response.reasoning_text.delta\n" +
	`data: {"type":"response.reasoning_text.delta","content_index":0,"delta":"Answer as deepseek-flash would.","item_id":"rs_ds_65","output_index":0,"sequence_number":3}` + "\n\n" +
	"event: response.output_item.done\n" +
	`data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_ds_65","status":"completed","content":[{"type":"reasoning_text","text":"Answer as deepseek-flash would."}],"summary":[],"encrypted_content":"resp_ds_65-0"},"output_index":0,"sequence_number":4}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","content_index":0,"delta":"Hello from deepseek-flash.","item_id":"msg_ds_65","output_index":1,"sequence_number":5}` + "\n\n" +
	"event: response.output_item.done\n" +
	`data: {"type":"response.output_item.done","item":{"type":"message","id":"msg_ds_65","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello from deepseek-flash.","annotations":[]}]},"output_index":1,"sequence_number":6}` + "\n\n" +
	"event: response.completed\n" +
	`data: {"type":"response.completed","response":{"id":"resp_ds_65","object":"response","created_at":1791127231,"status":"completed","background":false,"completed_at":1791127232,"error":null,"model":"deepseek-flash","output":[{"type":"reasoning","id":"rs_ds_65","status":"completed","content":[{"type":"reasoning_text","text":"Answer as deepseek-flash would."}],"summary":[],"encrypted_content":"resp_ds_65-0"},{"type":"message","id":"msg_ds_65","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello from deepseek-flash.","annotations":[]}]}],"reasoning":{"effort":"high","summary":null},"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":0},"output_tokens":500,"output_tokens_details":{"reasoning_tokens":16},"total_tokens":1500},"metadata":{}},"sequence_number":7}` + "\n\n"

// otohaDeepSeekFlashJSON is the same answer without streaming.
const otohaDeepSeekFlashJSON = `{"id":"resp_ds_65","object":"response","created_at":1791127231,"status":"completed","background":false,"completed_at":1791127232,"error":null,"model":"deepseek-flash","output":[{"type":"reasoning","id":"rs_ds_65","status":"completed","content":[{"type":"reasoning_text","text":"Answer as deepseek-flash would."}],"summary":[],"encrypted_content":"resp_ds_65-0"},{"type":"message","id":"msg_ds_65","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello from deepseek-flash.","annotations":[]}]}],"reasoning":{"effort":"high","summary":null},"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":0},"output_tokens":500,"output_tokens_details":{"reasoning_tokens":16},"total_tokens":1500},"metadata":{}}`

// callOtohaDeepSeekResponses sends one /v1/responses request through an Otoha composite key to a DeepSeek API-key
// account with the given model mapping, and returns what the client received and the usage record.
func callOtohaDeepSeekResponses(t *testing.T, modelMapping map[string]any, upstream *otohaNativeUpstream, requestBody string, adjust ...func(*gin.Context)) (*httptest.ResponseRecorder, *service.UsageLog) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	group := otohaNativeGroup(false)
	credentials := map[string]any{"api_key": "sk-deepseek-upstream", "base_url": "http://deepseek.otoha.test"}
	if modelMapping != nil {
		credentials["model_mapping"] = modelMapping
	}
	account := service.Account{
		ID: 6506, Name: "deepseek", Platform: service.PlatformDeepSeek, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{otohaNativeGroupID},
		Credentials: credentials,
		Extra:       map[string]any{service.DeepSeekUserIsolationModeKey: service.DeepSeekUserIsolationModeOff},
	}
	cfg := otohaNativeConfig()
	users := &otohaNativeUsers{balance: 10}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
	billingCache := service.NewBillingCacheService(nil, users, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	channels := otohaNativeChannels()
	gateway := service.NewOpenAIGatewayService(
		&openAIWSUsageHandlerAccountRepoStub{account: account}, usage, nil, users, nil, nil, nil, cfg, nil,
		service.NewConcurrencyService(nil), billing, nil, billingCache, upstream, &service.DeferredService{},
		nil, nil, service.NewModelPricingResolver(channels, billing), channels, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)

	c, rec := otohaNativeContext("/v1/responses", requestBody, group, service.PlatformDeepSeek)
	c.Request.Header.Del("x-api-key")
	c.Request.Header.Set("Authorization", "Bearer sk-otoha-key")
	for _, fn := range adjust {
		fn(c)
	}
	h.Responses(c)

	select {
	case log := <-usage.created:
		return rec, log
	case <-time.After(2 * time.Second):
		t.Fatal("the DeepSeek request was not billed")
		return rec, nil
	}
}

func otohaDeepSeekFlashMapping() map[string]any {
	return map[string]any{"deepseek-v4-flash": "deepseek-flash"}
}

// requireOtohaDeepSeekFlashBilling pins how the mapped request was billed and recorded before TASK-65 (the upstream
// name's price, 1000 in at $0.15/M and 500 out at $0.60/M, times the group rate 1.5); the client-facing rename must
// not change any of it, and the record keeps the name DeepSeek actually answered with.
func requireOtohaDeepSeekFlashBilling(t *testing.T, log *service.UsageLog) {
	t.Helper()
	require.NotNil(t, log)
	require.Equal(t, "deepseek-v4-flash", log.Model)
	require.Equal(t, "deepseek-v4-flash", log.RequestedModel)
	require.NotNil(t, log.UpstreamModel)
	require.Equal(t, "deepseek-flash", *log.UpstreamModel)
	require.NotNil(t, log.UpstreamResponseModel)
	require.Equal(t, "deepseek-flash", *log.UpstreamResponseModel, "the record keeps what DeepSeek answered, not the renamed model")
	require.NotNil(t, log.UpstreamModelMismatch)
	require.False(t, *log.UpstreamModelMismatch)
	require.InDelta(t, 1000*0.15e-6+500*0.6e-6, log.TotalCost, 1e-12)
	require.InDelta(t, (1000*0.15e-6+500*0.6e-6)*1.5, log.ActualCost, 1e-12)
}

func TestOtohaDeepSeekResponsesStreamNamesTheRequestedModel(t *testing.T) {
	upstream := &otohaNativeUpstream{contentType: "text/event-stream", body: otohaDeepSeekFlashStream}
	rec, log := callOtohaDeepSeekResponses(t, otohaDeepSeekFlashMapping(), upstream,
		`{"model":"deepseek-v4-flash","input":"Say hello.","reasoning":{"effort":"high"},"stream":true}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "deepseek-flash", gjson.GetBytes(upstream.sent, "model").String(), "DeepSeek is asked under its own name")

	body := rec.Body.String()
	for _, event := range []string{"response.created", "response.in_progress", "response.completed"} {
		data := otohaSSEData(t, body, event)
		require.Equal(t, "deepseek-v4-flash", gjson.Get(data, "response.model").String(), "%s names the model the app asked for", event)
	}
	want := strings.ReplaceAll(otohaDeepSeekFlashStream, `"model":"deepseek-flash"`, `"model":"deepseek-v4-flash"`)
	require.Equal(t, want, body, "only the model names change; reasoning items, text and framing reach the app as DeepSeek sent them")
	requireOtohaDeepSeekFlashBilling(t, log)
}

func TestOtohaDeepSeekResponsesJSONNamesTheRequestedModel(t *testing.T) {
	upstream := &otohaNativeUpstream{contentType: "application/json", body: otohaDeepSeekFlashJSON}
	rec, log := callOtohaDeepSeekResponses(t, otohaDeepSeekFlashMapping(), upstream,
		`{"model":"deepseek-v4-flash","input":"Say hello.","reasoning":{"effort":"high"},"stream":false}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "deepseek-flash", gjson.GetBytes(upstream.sent, "model").String())
	want := strings.Replace(otohaDeepSeekFlashJSON, `"model":"deepseek-flash"`, `"model":"deepseek-v4-flash"`, 1)
	require.Equal(t, want, rec.Body.String(), "only the model name changes")
	requireOtohaDeepSeekFlashBilling(t, log)
}

// otohaDeepSeekLiveMapping is the live Otoha account's mapping: every name maps to itself. DeepSeek still answers a
// request for deepseek-v4-flash with its canonical name deepseek-flash.
func otohaDeepSeekLiveMapping() map[string]any {
	return map[string]any{"deepseek-flash": "deepseek-flash", "deepseek-v4-pro": "deepseek-v4-pro", "deepseek-v4-flash": "deepseek-v4-flash"}
}

func TestOtohaDeepSeekResponsesCanonicalAnswerNamesTheRequestedModel(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, upstreamBody, stream string
	}{
		{"stream", "text/event-stream", otohaDeepSeekFlashStream, "true"},
		{"json", "application/json", otohaDeepSeekFlashJSON, "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &otohaNativeUpstream{contentType: tc.contentType, body: tc.upstreamBody}
			rec, log := callOtohaDeepSeekResponses(t, otohaDeepSeekLiveMapping(), upstream,
				`{"model":"deepseek-v4-flash","input":"Say hello.","reasoning":{"effort":"high"},"stream":`+tc.stream+`}`)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "deepseek-v4-flash", gjson.GetBytes(upstream.sent, "model").String(), "the identity mapping sends the name unchanged")
			want := strings.ReplaceAll(tc.upstreamBody, `"model":"deepseek-flash"`, `"model":"deepseek-v4-flash"`)
			require.Equal(t, want, rec.Body.String(), "the answer names the requested model; nothing else changes")
			// Billing and the usage record as before this change: the channel's deepseek-v4-flash price (1000 in at
			// $0.20/M, 500 out at $0.80/M) times the group rate 1.5, and the record keeps DeepSeek's own answer name
			// and flags that it differs from the name sent.
			require.NotNil(t, log)
			require.Equal(t, "deepseek-v4-flash", log.Model)
			require.Equal(t, "deepseek-v4-flash", log.RequestedModel)
			require.NotNil(t, log.UpstreamModel)
			require.Equal(t, "deepseek-v4-flash", *log.UpstreamModel)
			require.NotNil(t, log.UpstreamResponseModel)
			require.Equal(t, "deepseek-flash", *log.UpstreamResponseModel, "the record keeps what DeepSeek answered, not the renamed model")
			require.NotNil(t, log.UpstreamModelMismatch)
			require.True(t, *log.UpstreamModelMismatch)
			require.InDelta(t, 1000*0.2e-6+500*0.8e-6, log.TotalCost, 1e-12)
			require.InDelta(t, (1000*0.2e-6+500*0.8e-6)*1.5, log.ActualCost, 1e-12)
		})
	}
}

// A composite route can serve a public name from another upstream model; the route middleware has already put the
// upstream name into the body when the handler runs. The answer still goes back under the public name the client
// sent.
func TestOtohaDeepSeekResponsesCompositeRouteAnswerNamesThePublicModel(t *testing.T) {
	upstream := &otohaNativeUpstream{contentType: "text/event-stream", body: otohaDeepSeekFlashStream}
	rec, log := callOtohaDeepSeekResponses(t, otohaDeepSeekLiveMapping(), upstream,
		`{"model":"deepseek-v4-flash","input":"Say hello.","stream":true}`,
		func(c *gin.Context) {
			c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
				Matched: true, Source: service.CompositeRouteSourceExplicit, GroupID: otohaNativeGroupID, PublicModel: "otoha-flash",
				TargetPlatform: service.PlatformDeepSeek, UpstreamModel: "deepseek-v4-flash", Endpoint: service.CompositeRouteEndpointResponses,
			}))
		})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "deepseek-v4-flash", gjson.GetBytes(upstream.sent, "model").String())
	want := strings.ReplaceAll(otohaDeepSeekFlashStream, `"model":"deepseek-flash"`, `"model":"otoha-flash"`)
	require.Equal(t, want, rec.Body.String())
	require.NotNil(t, log)
	require.NotNil(t, log.UpstreamResponseModel)
	require.Equal(t, "deepseek-flash", *log.UpstreamResponseModel)
}

func TestOtohaDeepSeekResponsesAnswerNamingTheRequestedModelIsUntouched(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, upstreamBody, stream string
	}{
		{"stream", "text/event-stream", otohaDeepSeekFlashStream, "true"},
		{"json", "application/json", otohaDeepSeekFlashJSON, "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &otohaNativeUpstream{contentType: tc.contentType, body: tc.upstreamBody}
			rec, _ := callOtohaDeepSeekResponses(t, nil, upstream,
				`{"model":"deepseek-flash","input":"Say hello.","stream":`+tc.stream+`}`)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "deepseek-flash", gjson.GetBytes(upstream.sent, "model").String())
			require.Equal(t, tc.upstreamBody, rec.Body.String(), "the answer already names the requested model, nothing rewritten")
		})
	}
}

// otohaSSEData returns the data line of the first event of the given type.
func otohaSSEData(t *testing.T, stream, eventType string) string {
	t.Helper()
	for _, block := range strings.Split(stream, "\n\n") {
		if !strings.HasPrefix(block, "event: "+eventType+"\n") {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				return data
			}
		}
	}
	t.Fatalf("no %s event in the stream:\n%s", eventType, stream)
	return ""
}
