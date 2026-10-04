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
	h.SetOtohaCatalog(otohaCatalogReaderStub{catalog: &service.OtohaCatalog{Schema: 1, Revision: "abc", Models: []service.OtohaCatalogModel{}}})

	rec := otohaModelsRequest(h, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `"abc"`, rec.Header().Get("ETag"))
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"schema":1,"revision":"abc","models":[]}`, rec.Body.String())

	require.Equal(t, http.StatusNotModified, otohaModelsRequest(h, map[string]string{"If-None-Match": `"abc"`}).Code)
	require.Equal(t, http.StatusOK, otohaModelsRequest(h, map[string]string{"If-None-Match": `"older"`}).Code, "a new revision is sent")
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
