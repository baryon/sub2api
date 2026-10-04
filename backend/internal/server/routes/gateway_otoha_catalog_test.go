package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// GET /v1/models?client=otoha gives an Otoha app the catalog of its key's group (TASK-54).

type otohaRouteKeyRepo struct {
	service.APIKeyRepository
	keys map[string]*service.APIKey
}

func (r *otohaRouteKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	apiKey, ok := r.keys[key]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *apiKey
	return &clone, nil
}

func (r *otohaRouteKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

type otohaRouteCatalogReader struct {
	mu       sync.Mutex
	asked    []int64
	rates    []float64
	catalogs map[int64]*service.OtohaCatalog
}

func (r *otohaRouteCatalogReader) CatalogForGroup(_ context.Context, groupID int64) (*service.OtohaCatalog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, groupID)
	return r.catalogs[groupID], nil
}

func (r *otohaRouteCatalogReader) CatalogForGroupAtRate(_ context.Context, groupID int64, rate float64) (*service.OtohaCatalog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, groupID)
	r.rates = append(r.rates, rate)
	return r.catalogs[groupID], nil
}

// User 7 has a rate of their own in group 42.
type otohaRouteUserRateRepo struct {
	service.UserGroupRateRepository
}

func (otohaRouteUserRateRepo) GetByUserAndGroup(_ context.Context, userID, groupID int64) (*float64, error) {
	if userID == 7 && groupID == 42 {
		rate := 0.4
		return &rate, nil
	}
	return nil, nil
}

func (otohaRouteUserRateRepo) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	return nil, nil
}

var otohaRouteCatalog = &service.OtohaCatalog{
	Schema:   1,
	Revision: "rev-1",
	Models: []service.OtohaCatalogModel{{
		ID: "claude-sonnet-4-5", Name: "Claude Sonnet", Inputs: []string{"text", "image"}, Images: true, Tools: true,
		Cost:  "standard",
		Price: service.OtohaModelPrice{Currency: "USD", Per: "1M tokens", Input: 3, Output: 15},
	}},
}

const (
	otohaRouteCatalogKey  = "otoha-catalog-key"
	otohaRoutePlainKey    = "otoha-plain-key"
	otohaRouteNoCreditKey = "otoha-no-credit-key"
)

func newOtohaCatalogRouteTestRouter(t *testing.T) (*gin.Engine, *otohaRouteCatalogReader) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	catalogGroup := &service.Group{ID: 42, Status: service.StatusActive, Hydrated: true, Platform: service.PlatformAnthropic, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	plainGroup := &service.Group{ID: 43, Status: service.StatusActive, Hydrated: true, Platform: service.PlatformAnthropic, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	user := &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10}
	broke := &service.User{ID: 8, Role: service.RoleUser, Status: service.StatusActive, Balance: 0}
	key := func(id int64, value string, owner *service.User, group *service.Group) *service.APIKey {
		groupID := group.ID
		return &service.APIKey{ID: id, UserID: owner.ID, Key: value, Status: service.StatusActive, User: owner, GroupID: &groupID, Group: group}
	}
	repo := &otohaRouteKeyRepo{keys: map[string]*service.APIKey{
		otohaRouteCatalogKey:  key(100, otohaRouteCatalogKey, user, catalogGroup),
		otohaRoutePlainKey:    key(101, otohaRoutePlainKey, user, plainGroup),
		otohaRouteNoCreditKey: key(102, otohaRouteNoCreditKey, broke, catalogGroup),
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	gatewayService := service.NewGatewayService(
		&gatewayModelsAccountRepoStubForOtoha{}, nil, nil, nil, nil, nil, otohaRouteUserRateRepo{}, nil, cfg, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	gatewayHandler := handler.NewGatewayHandler(
		gatewayService, nil, nil, nil, nil, nil, nil, nil,
		apiKeyService, nil, nil, nil, nil, cfg, nil,
	)
	reader := &otohaRouteCatalogReader{catalogs: map[int64]*service.OtohaCatalog{42: otohaRouteCatalog}}
	gatewayHandler.SetOtohaCatalog(reader)

	router := gin.New()
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{Gateway: gatewayHandler, OpenAIGateway: &handler.OpenAIGatewayHandler{}},
		servermiddleware.NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg),
		apiKeyService, nil, nil, nil, nil, cfg,
	)
	return router, reader
}

type gatewayModelsAccountRepoStubForOtoha struct {
	service.AccountRepository
}

func (gatewayModelsAccountRepoStubForOtoha) ListSchedulableByGroupID(context.Context, int64) ([]service.Account, error) {
	return []service.Account{{ID: 1, Platform: service.PlatformAnthropic, Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"},
	}}}, nil
}

func otohaRouteGet(router *gin.Engine, path, key string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestOtohaCatalogRouteNeedsAValidKey(t *testing.T) {
	router, reader := newOtohaCatalogRouteTestRouter(t)

	require.Equal(t, http.StatusUnauthorized, otohaRouteGet(router, "/v1/models?client=otoha", "", nil).Code)
	require.Equal(t, http.StatusUnauthorized, otohaRouteGet(router, "/v1/models?client=otoha", "not-a-key", nil).Code)
	require.Empty(t, reader.asked, "no catalog is read without a valid key")
}

func TestOtohaCatalogRouteGivesTheKeysGroupCatalog(t *testing.T) {
	router, reader := newOtohaCatalogRouteTestRouter(t)

	for _, path := range []string{"/v1/models?client=otoha", "/models?client=otoha"} {
		rec := otohaRouteGet(router, path, otohaRouteCatalogKey, nil)
		require.Equal(t, http.StatusOK, rec.Code, path)
		var got service.OtohaCatalog
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Equal(t, *otohaRouteCatalog, got)
		require.Equal(t, `"rev-1"`, rec.Header().Get("ETag"))
	}
	require.Equal(t, []int64{42, 42}, reader.asked, "only the key's own group is read")
	require.Equal(t, []float64{0.4, 0.4}, reader.rates, "priced at the rate this user pays in the group")

	rec := otohaRouteGet(router, "/v1/models?client=otoha", otohaRouteCatalogKey, map[string]string{"If-None-Match": `"rev-1"`})
	require.Equal(t, http.StatusNotModified, rec.Code)
	require.Empty(t, rec.Body.Bytes())
}

func TestOtohaCatalogRouteLeavesGroupsWithoutACatalogAsTheyWere(t *testing.T) {
	router, _ := newOtohaCatalogRouteTestRouter(t)

	plain := otohaRouteGet(router, "/v1/models", otohaRoutePlainKey, nil)
	otoha := otohaRouteGet(router, "/v1/models?client=otoha", otohaRoutePlainKey, nil)
	require.Equal(t, http.StatusOK, plain.Code)
	require.Equal(t, http.StatusOK, otoha.Code)
	require.JSONEq(t, plain.Body.String(), otoha.Body.String())

	withCatalogPlain := otohaRouteGet(router, "/v1/models", otohaRouteCatalogKey, nil)
	require.JSONEq(t, plain.Body.String(), withCatalogPlain.Body.String(), "without client=otoha a catalog group lists as before")
}

func TestOtohaCatalogRouteStaysReadableWhenCreditRunsOut(t *testing.T) {
	router, reader := newOtohaCatalogRouteTestRouter(t)

	rec := otohaRouteGet(router, "/v1/models?client=otoha", otohaRouteNoCreditKey, nil)
	require.Equal(t, http.StatusOK, rec.Code, "the app still shows models and prices to a user who has to buy more")
	var got service.OtohaCatalog
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "rev-1", got.Revision)
	require.Equal(t, []float64{1}, reader.rates, "a user without a rate of their own pays the group's")

	require.Equal(t, http.StatusForbidden, otohaRouteGet(router, "/v1/models", otohaRouteNoCreditKey, nil).Code,
		"the plain model list keeps its billing check")
}

// Only admins read or change a group's catalog.
func TestOtohaCatalogAdminRoutesRequireAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/groups/7/otoha-catalog"},
		{http.MethodPost, "/api/v1/admin/groups/7/otoha-catalog/entries"},
		{http.MethodPut, "/api/v1/admin/groups/7/otoha-catalog/entries/1"},
		{http.MethodDelete, "/api/v1/admin/groups/7/otoha-catalog/entries/1"},
		{http.MethodPut, "/api/v1/admin/groups/7/otoha-catalog/order"},
		{http.MethodPost, "/api/v1/admin/groups/7/otoha-catalog/prefill"},
	} {
		for _, tc := range []struct {
			auth string
			want int
		}{{"", http.StatusUnauthorized}, {"Bearer user-token", http.StatusForbidden}} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(route.method, route.path, nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.want, rec.Code, "%s %s auth=%q", route.method, route.path, tc.auth)
		}
	}
}
