package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TASK-64: the Otoha app calls Claude on POST /v1/messages with the same key it uses on /v1/responses, sent the way
// Anthropic clients send it (x-api-key). The key's checks apply there as on /v1/responses: the group's model
// allowlist, and the plan-then-balance credit of an Otoha group.

const (
	otohaNativeRouteKey      = "otoha-native-key"
	otohaNativeRouteBrokeKey = "otoha-native-broke-key"
)

func newOtohaNativeRouteRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	group := &service.Group{
		ID: 52, Status: service.StatusActive, Hydrated: true, Platform: service.PlatformComposite, RateMultiplier: 1,
		SubscriptionType: service.SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"claude-sonnet-4-5", "deepseek-v4-flash"}},
	}
	user := &service.User{ID: 17, Role: service.RoleUser, Status: service.StatusActive, Balance: 10}
	broke := &service.User{ID: 18, Role: service.RoleUser, Status: service.StatusActive, Balance: 0}
	key := func(id int64, value string, owner *service.User) *service.APIKey {
		groupID := group.ID
		return &service.APIKey{ID: id, UserID: owner.ID, Key: value, Status: service.StatusActive, User: owner, GroupID: &groupID, Group: group}
	}
	repo := &otohaRouteKeyRepo{keys: map[string]*service.APIKey{
		otohaNativeRouteKey:      key(200, otohaNativeRouteKey, user),
		otohaNativeRouteBrokeKey: key(201, otohaNativeRouteBrokeKey, broke),
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.MaxBodySize = 1 << 20
	cfg.Gateway.TextMaxBodySize = 1 << 20
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := gin.New()
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}},
		servermiddleware.NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg),
		apiKeyService, nil, nil, nil, nil, cfg,
	)
	return router
}

func otohaNativeMessagesRequest(router *gin.Engine, key, model string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"`+model+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestOtohaMessagesTakesTheKeyAsAnthropicClientsSendIt(t *testing.T) {
	router := newOtohaNativeRouteRouter(t)

	rec := otohaNativeMessagesRequest(router, "not-a-key", "claude-sonnet-4-5")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = otohaNativeMessagesRequest(router, otohaNativeRouteKey, "grok-4.7")
	require.Equal(t, http.StatusNotFound, rec.Code, "the group's allowlist applies on /v1/messages: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "grok-4.7")
}

func TestOtohaMessagesRefusesWhenPlanAndBalanceAreUsedUp(t *testing.T) {
	router := newOtohaNativeRouteRouter(t)

	rec := otohaNativeMessagesRequest(router, otohaNativeRouteBrokeKey, "claude-sonnet-4-5")
	require.Equal(t, http.StatusPaymentRequired, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "CREDIT_EXHAUSTED")
}

// In a composite group without explicit routes, each model goes on its native endpoint to the gateway that serves its
// provider: Claude on /v1/messages to the Anthropic gateway, DeepSeek, GPT and Grok on /v1/responses to the OpenAI
// gateway, under the provider the composite resolver picks by name.
func TestOtohaCompositeModelsReachTheirGatewayOnTheirNativeEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(53)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
		})
		c.Next()
	})))
	router.Use(compositeTargetPlatformMiddleware(service.NewCompositeRouteResolver(compositeRouteRepoStub{})))
	mark := func(gateway string) gin.HandlerFunc {
		return func(c *gin.Context) {
			platform, _ := service.ResolvedTargetPlatformFromContext(c.Request.Context())
			endpoint, _ := service.CompositeRouteEndpointFromContext(c.Request.Context())
			c.String(http.StatusOK, gateway+":"+platform+":"+endpoint)
		}
	}
	router.POST("/v1/messages", func(c *gin.Context) { dispatchMessagesGateway(c, mark("openai"), mark("anthropic")) })
	router.POST("/v1/responses", func(c *gin.Context) { dispatchChatResponsesGateway(c, mark("openai"), mark("anthropic")) })

	for _, tc := range []struct{ path, model, want string }{
		{"/v1/messages", "claude-sonnet-4-5", "anthropic:anthropic:messages"},
		{"/v1/responses", "deepseek-v4-flash", "openai:deepseek:responses"},
		{"/v1/responses", "gpt-6-luna", "openai:openai:responses"},
		{"/v1/responses", "grok-4.7", "openai:grok:responses"},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"`+tc.model+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, tc.model)
		require.Equal(t, tc.want, rec.Body.String(), tc.model)
	}
}
