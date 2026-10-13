package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type otohaCatalogReaderStub struct {
	catalog *service.OtohaCatalog
	err     error
}

func (s otohaCatalogReaderStub) CatalogForGroup(context.Context, int64) (*service.OtohaCatalog, error) {
	return s.catalog, s.err
}

func otohaModelsRequest(h *GatewayHandler, header map[string]string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client=otoha", nil)
	for k, v := range header {
		c.Request.Header.Set(k, v)
	}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{ID: 5, Platform: service.PlatformAnthropic}})
	h.OtohaModels(c)
	return rec
}

func TestOtohaModelsETagFollowsTheRevision(t *testing.T) {
	h := &GatewayHandler{}
	h.SetOtohaCatalog(otohaCatalogReaderStub{catalog: &service.OtohaCatalog{Schema: 1, Revision: "abc", Models: []service.OtohaCatalogModel{}, ImageModels: []service.OtohaImageModel{},
		VideoModels: []service.OtohaVideoModel{}, VoiceModels: []service.OtohaVoiceModel{}, QuickModels: []service.OtohaQuickModel{}}})

	rec := otohaModelsRequest(h, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `"abc"`, rec.Header().Get("ETag"))
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"schema":1,"revision":"abc","models":[],"imageModels":[],"videoModels":[],"voiceModels":[],"quickModels":[]}`, rec.Body.String())

	require.Equal(t, http.StatusNotModified, otohaModelsRequest(h, map[string]string{"If-None-Match": `"abc"`}).Code)
	require.Equal(t, http.StatusOK, otohaModelsRequest(h, map[string]string{"If-None-Match": `"older"`}).Code, "a new revision is sent")
}

func TestOtohaModelsListsImageModelsApartFromTheChatModels(t *testing.T) {
	h := &GatewayHandler{}
	h.SetOtohaCatalog(otohaCatalogReaderStub{catalog: &service.OtohaCatalog{
		Schema: 1, Revision: "img1",
		Models: []service.OtohaCatalogModel{{ID: "gpt-6-luna", Name: "Luna"}},
		ImageModels: []service.OtohaImageModel{{
			ID: "gpt-image-2", Name: "GPT Image 2", API: service.OtohaImageAPI, Edits: true, Qualities: []string{"low", "high"},
			Currency: "USD", Per: "image",
			Sizes: []service.OtohaImageSize{{Size: "1024x1024", Tier: "1K", Price: 0.06}, {Size: "1536x1024", Tier: "2K", Price: 0.09}},
		}},
	}})

	rec := otohaModelsRequest(h, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Models      []map[string]any `json:"models"`
		ImageModels []map[string]any `json:"imageModels"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Models, 1)
	require.Equal(t, "gpt-6-luna", body.Models[0]["id"], "an older app, which reads only models, sees no image model")
	require.Len(t, body.ImageModels, 1)
	require.Equal(t, map[string]any{
		"id": "gpt-image-2", "name": "GPT Image 2", "api": "openai-images", "edits": true,
		"qualities": []any{"low", "high"}, "currency": "USD", "per": "image",
		"sizes": []any{
			map[string]any{"size": "1024x1024", "tier": "1K", "price": 0.06},
			map[string]any{"size": "1536x1024", "tier": "2K", "price": 0.09},
		},
	}, body.ImageModels[0])
}

func TestOtohaModelsReportsAnUnreadableCatalog(t *testing.T) {
	h := &GatewayHandler{}
	h.SetOtohaCatalog(otohaCatalogReaderStub{err: errors.New("db down")})

	rec := otohaModelsRequest(h, nil)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "the app keeps the catalog it has rather than a plain list")
	require.NotContains(t, rec.Body.String(), "db down")
}

// ---- /v1/usage?client=otoha ----

type otohaUsageRepoStub struct {
	service.UsageLogRepository
	starts   []time.Time
	apiKeyID int64
	stats    []usagestats.ModelStat
}

func (r *otohaUsageRepoStub) GetModelStatsWithFilters(_ context.Context, start, end time.Time, _, apiKeyID, _, _ int64, _ *int16, _ *bool, _ *int8) ([]usagestats.ModelStat, error) {
	r.starts = append(r.starts, start)
	r.apiKeyID = apiKeyID
	return r.stats, nil
}

func (r *otohaUsageRepoStub) GetAPIKeyDashboardStats(context.Context, int64) (*usagestats.UserDashboardStats, error) {
	return nil, errors.New("not needed")
}

func (r *otohaUsageRepoStub) GetUsageTrendWithFilters(context.Context, time.Time, time.Time, string, int64, int64, int64, int64, string, *int16, *bool, *int8) ([]usagestats.TrendDataPoint, error) {
	return nil, errors.New("not needed")
}

func otohaUsageRequest(t *testing.T, path string, plan *service.UserSubscription, repo *otohaUsageRepoStub) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	if plan != nil {
		c.Set(string(middleware.ContextKeySubscription), plan)
	}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 31, Status: service.StatusActive, Group: &service.Group{
		ID: 5, Name: "Otoha", SubscriptionType: service.SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
	}})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
	h := &GatewayHandler{
		userService:  service.NewUserService(usageUserRepoStub{balance: 3}, nil, nil, nil),
		usageService: service.NewUsageService(repo, nil, nil, nil),
	}
	h.Usage(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	return response
}

func TestOtohaUsageAddsThisPeriodsUsagePerModel(t *testing.T) {
	windowStart := time.Now().Add(-5 * 24 * time.Hour).UTC().Truncate(time.Second)
	plan := &service.UserSubscription{
		StartsAt: windowStart, ExpiresAt: windowStart.Add(365 * 24 * time.Hour), MonthlyWindowStart: &windowStart,
	}
	repo := &otohaUsageRepoStub{stats: []usagestats.ModelStat{
		{Model: "gpt-6-luna", Requests: 4, InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200, Cost: 0.5, ActualCost: 0.75, AccountCost: 0.2},
	}}
	response := otohaUsageRequest(t, "/v1/usage?client=otoha", plan, repo)

	require.Equal(t, int64(31), repo.apiKeyID, "the key's own usage")
	fromWindow := false
	for _, start := range repo.starts {
		fromWindow = fromWindow || start.Equal(windowStart)
	}
	require.True(t, fromWindow, "read from the plan's monthly window")

	period, ok := response["period"].(map[string]any)
	require.True(t, ok, "response: %v", response)
	startText, ok := period["start"].(string)
	require.True(t, ok)
	start, err := time.Parse(time.RFC3339, startText)
	require.NoError(t, err)
	require.True(t, start.Equal(windowStart))
	endText, ok := period["end"].(string)
	require.True(t, ok)
	end, err := time.Parse(time.RFC3339, endText)
	require.NoError(t, err)
	require.True(t, end.Equal(windowStart.Add(30*24*time.Hour)))

	usage, ok := response["model_usage"].([]any)
	require.True(t, ok)
	require.Len(t, usage, 1)
	row, ok := usage[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "gpt-6-luna", row["model"])
	require.Equal(t, 4.0, row["requests"])
	require.Equal(t, 1200.0, row["total_tokens"])
	require.Equal(t, 0.75, row["cost"], "what the user paid")
	require.NotContains(t, row, "account_cost")

	require.Equal(t, 3.0, response["balance"], "the existing fields stay")
	require.Contains(t, response, "mode")
}

func TestOtohaUsageWithoutAnyUseThisPeriodIsAnEmptyList(t *testing.T) {
	response := otohaUsageRequest(t, "/v1/usage?client=otoha", nil, &otohaUsageRepoStub{})
	usage, ok := response["model_usage"].([]any)
	require.True(t, ok)
	require.Empty(t, usage)
}

func TestUsageWithoutTheOtohaClientIsUnchanged(t *testing.T) {
	response := otohaUsageRequest(t, "/v1/usage", nil, &otohaUsageRepoStub{})
	require.NotContains(t, response, "period")
	require.NotContains(t, response, "model_usage")
}

func TestOtohaModelsCarriesPriceTiersAndCompaction(t *testing.T) {
	cached := 0.5
	h := &GatewayHandler{}
	h.SetOtohaCatalog(otohaCatalogReaderStub{catalog: &service.OtohaCatalog{Schema: 1, Revision: "r1", Models: []service.OtohaCatalogModel{
		{
			ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5", Inputs: []string{"text"}, Cost: "low",
			Price: service.OtohaModelPrice{Currency: "USD", Per: "1M tokens", Input: 1, Output: 5},
			PriceTiers: []service.OtohaPriceTier{
				{Input: 1, Output: 5},
				{AboveInputTokens: 100000, Input: 5, Output: 25, CachedInput: &cached},
			},
			Compaction: service.OtohaCompaction{Kind: "anthropic", MinTrigger: 50000},
		},
		{
			ID: "grok-4.5", Name: "Grok 4.5", Inputs: []string{"text"}, Cost: "low",
			Price: service.OtohaModelPrice{Currency: "USD", Per: "1M tokens", Input: 2, Output: 6},
			PriceTiers: []service.OtohaPriceTier{
				{Input: 2, Output: 6},
				{AboveInputTokens: 200000, Inclusive: true, Input: 4, Output: 12},
			},
			Compaction: service.OtohaCompaction{Kind: "none"},
		},
		{
			ID: "plain", Name: "Plain", Inputs: []string{"text"}, Cost: "low",
			Price:      service.OtohaModelPrice{Currency: "USD", Per: "1M tokens", Input: 1, Output: 2},
			PriceTiers: []service.OtohaPriceTier{{Input: 1, Output: 2}},
			Compaction: service.OtohaCompaction{Kind: "none"},
		},
	}}})

	rec := otohaModelsRequest(h, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Models, 3)

	require.JSONEq(t, `[{"aboveInputTokens":0,"inclusive":false,"input":1,"output":5},
		{"aboveInputTokens":100000,"inclusive":false,"input":5,"output":25,"cachedInput":0.5}]`, string(body.Models[0]["priceTiers"]))
	require.JSONEq(t, `{"kind":"anthropic","minTrigger":50000}`, string(body.Models[0]["compaction"]))
	require.JSONEq(t, `[{"aboveInputTokens":0,"inclusive":false,"input":2,"output":6},
		{"aboveInputTokens":200000,"inclusive":true,"input":4,"output":12}]`, string(body.Models[1]["priceTiers"]))
	require.JSONEq(t, `[{"aboveInputTokens":0,"inclusive":false,"input":1,"output":2}]`, string(body.Models[2]["priceTiers"]))
	require.JSONEq(t, `{"kind":"none"}`, string(body.Models[2]["compaction"]))
	require.JSONEq(t, `{"currency":"USD","per":"1M tokens","input":1,"output":5}`, string(body.Models[0]["price"]), "price is unchanged for older apps")
}

func TestOtohaModelsCarriesThinkingAndLevels(t *testing.T) {
	yes := false
	h := &GatewayHandler{}
	h.SetOtohaCatalog(otohaCatalogReaderStub{catalog: &service.OtohaCatalog{Schema: 1, Revision: "r1", Models: []service.OtohaCatalogModel{
		{
			ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5", Inputs: []string{"text"}, Cost: "standard", Complexity: "complex",
			Thinking: &service.OtohaThinking{CanBeOff: &yes, Settings: []service.OtohaThinkingSetting{
				{Value: "adaptive"}, {Value: "between_tools", MaxEffort: "high"},
			}},
			Levels: []service.OtohaThinkingLevel{{Setting: "adaptive", Effort: "high", Complexity: "complex", ExtraOutput: "high"}},
		},
		{ID: "plain", Name: "Plain", Inputs: []string{"text"}, Cost: "low", Levels: []service.OtohaThinkingLevel{}},
	}}})

	rec := otohaModelsRequest(h, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.JSONEq(t, `{"canBeOff":false,"settings":[{"value":"adaptive"},{"value":"between_tools","maxEffort":"high"}]}`, string(body.Models[0]["thinking"]))
	require.JSONEq(t, `[{"setting":"adaptive","effort":"high","complexity":"complex","extraOutput":"high"}]`, string(body.Models[0]["levels"]))
	require.JSONEq(t, `"complex"`, string(body.Models[0]["complexity"]))
	require.JSONEq(t, `null`, string(body.Models[1]["thinking"]))
	require.JSONEq(t, `[]`, string(body.Models[1]["levels"]))
}

type otohaAutoTopUpStateStub struct {
	state  *service.OtohaAutoTopUp
	err    error
	userID int64
}

func (s *otohaAutoTopUpStateStub) State(_ context.Context, userID int64) (*service.OtohaAutoTopUp, error) {
	s.userID = userID
	return s.state, s.err
}

func otohaUsageRequestWithAutoTopUp(t *testing.T, path string, reader *otohaAutoTopUpStateStub) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 31, UserID: 9, Status: service.StatusActive, Group: &service.Group{
		ID: 5, Name: "Otoha", SubscriptionType: service.SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
	}})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
	h := &GatewayHandler{
		userService:  service.NewUserService(usageUserRepoStub{balance: 3}, nil, nil, nil),
		usageService: service.NewUsageService(&otohaUsageRepoStub{}, nil, nil, nil),
	}
	if reader != nil {
		h.SetOtohaAutoTopUp(reader)
	}
	h.Usage(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	return response
}

// The app holds only an API key, so it reads automatic top-up from its usage answer (TASK-78, OtohaAI OAI-643).
func TestOtohaUsageCarriesTheKeyOwnersAutomaticTopUp(t *testing.T) {
	reader := &otohaAutoTopUpStateStub{state: &service.OtohaAutoTopUp{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100}}
	response := otohaUsageRequestWithAutoTopUp(t, "/v1/usage?client=otoha", reader)

	require.Equal(t, int64(9), reader.userID, "the key owner's settings")
	topUp, ok := response["auto_topup"].(map[string]any)
	require.True(t, ok, "response: %v", response)
	require.Equal(t, true, topUp["enabled"])
	require.Equal(t, 5.0, topUp["threshold_usd"])
	require.Equal(t, 20.0, topUp["amount_usd"])
	require.Equal(t, 100.0, topUp["monthly_cap_usd"])
}

func TestOtohaUsageWithoutReadableAutomaticTopUpLeavesItNull(t *testing.T) {
	response := otohaUsageRequestWithAutoTopUp(t, "/v1/usage?client=otoha", &otohaAutoTopUpStateStub{err: errors.New("db down")})
	require.Contains(t, response, "auto_topup")
	require.Nil(t, response["auto_topup"])
	require.Contains(t, response, "model_usage", "the rest of the answer stays")

	response = otohaUsageRequestWithAutoTopUp(t, "/v1/usage?client=otoha", nil)
	require.Nil(t, response["auto_topup"], "a server without automatic top-up")
}

func TestUsageWithoutTheOtohaClientHasNoAutomaticTopUp(t *testing.T) {
	reader := &otohaAutoTopUpStateStub{state: &service.OtohaAutoTopUp{Enabled: true}}
	response := otohaUsageRequestWithAutoTopUp(t, "/v1/usage", reader)
	require.NotContains(t, response, "auto_topup")
	require.Zero(t, reader.userID, "not read")
}
