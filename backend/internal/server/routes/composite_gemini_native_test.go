package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TASK-87: the Otoha app (SwiftAgent's GeminiProvider) speaks Google's generateContent protocol on /v1beta with an
// Otoha key. These tests pin the part of the chain in front of the Gemini handlers for a composite group: which models
// reach a Gemini account, what everything else is answered, and that the group's model allowlist is applied first.

type compositeGeminiChain struct {
	router    *gin.Engine
	reached   int
	platform  string
	upstream  string
	allowlist service.GroupModelAllowlist
}

func newCompositeGeminiChain(t *testing.T, routes []service.CompositeModelRoute, allowlist service.GroupModelAllowlist) *compositeGeminiChain {
	t.Helper()
	gin.SetMode(gin.TestMode)
	chain := &compositeGeminiChain{router: gin.New(), allowlist: allowlist}
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{routes: routes})
	chain.router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(2)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite, ModelAllowlist: allowlist},
		})
		c.Next()
	})))
	chain.router.Use(servermiddleware.GroupModelAllowlist())
	chain.router.Use(compositeGeminiTargetPlatformMiddleware(resolver))
	record := func(c *gin.Context) {
		chain.reached++
		chain.platform, _ = service.ResolvedTargetPlatformFromContext(c.Request.Context())
		chain.upstream, _ = service.ResolvedUpstreamModelFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	}
	chain.router.GET("/v1beta/models", record)
	chain.router.GET("/v1beta/models/:model", record)
	chain.router.POST("/v1beta/models/*modelAction", record)
	return chain
}

func (c *compositeGeminiChain) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.router.ServeHTTP(w, req)
	return w
}

func TestCompositeGeminiNativeCallsReachTheGeminiAccountsOfAKnownModel(t *testing.T) {
	chain := newCompositeGeminiChain(t, nil, service.GroupModelAllowlist{})

	for _, action := range []string{"generateContent", "streamGenerateContent?alt=sse", "countTokens"} {
		chain.reached = 0
		w := chain.do(http.MethodPost, "/v1beta/models/gemini-3.5-flash:"+action, `{"contents":[]}`)
		require.Equal(t, http.StatusNoContent, w.Code, action)
		require.Equal(t, 1, chain.reached, action)
		require.Equal(t, service.PlatformGemini, chain.platform, action)
	}

	chain.reached = 0
	require.Equal(t, http.StatusNoContent, chain.do(http.MethodGet, "/v1beta/models/gemini-3.5-flash", "").Code)
	require.Equal(t, service.PlatformGemini, chain.platform)
	require.Equal(t, 1, chain.reached)
}

// The model list has no model in its path: it is the Gemini side of the group.
func TestCompositeGeminiModelListIsTheGeminiSideOfTheGroup(t *testing.T) {
	chain := newCompositeGeminiChain(t, nil, service.GroupModelAllowlist{})

	w := chain.do(http.MethodGet, "/v1beta/models", "")

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, service.PlatformGemini, chain.platform)
}

func TestCompositeGeminiAliasRouteForwardsTheUpstreamModel(t *testing.T) {
	chain := newCompositeGeminiChain(t, []service.CompositeModelRoute{{
		ID: 1, GroupID: 2, PublicModel: "otoha-fast", MatchType: service.CompositeRouteMatchExact,
		TargetPlatform: service.PlatformGemini, UpstreamModel: "gemini-3.5-flash", Endpoint: service.CompositeRouteEndpointGemini,
		Priority: 100, Enabled: true,
	}}, service.GroupModelAllowlist{})

	w := chain.do(http.MethodPost, "/v1beta/models/otoha-fast:generateContent", `{"contents":[]}`)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, service.PlatformGemini, chain.platform)
	require.Equal(t, "gemini-3.5-flash", chain.upstream)
}

// A model no provider of the group claims is unknown: it is answered 404 in Google's error format before any account
// is asked, instead of being sent to Google under a name the group never offered.
func TestCompositeGeminiUnknownModelIsNotFound(t *testing.T) {
	chain := newCompositeGeminiChain(t, nil, service.GroupModelAllowlist{})

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1beta/models/not-a-model:generateContent", `{"contents":[]}`},
		{http.MethodPost, "/v1beta/models/not-a-model:streamGenerateContent?alt=sse", `{"contents":[]}`},
		{http.MethodGet, "/v1beta/models/not-a-model", ""},
	} {
		w := chain.do(tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusNotFound, w.Code, tc.path)
		require.Equal(t, "NOT_FOUND", gjson.Get(w.Body.String(), "error.status").String(), tc.path)
		require.EqualValues(t, 404, gjson.Get(w.Body.String(), "error.code").Int(), tc.path)
		require.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "not-a-model", tc.path)
	}
	require.Zero(t, chain.reached)
}

// A model of another provider is not served by Google's protocol.
func TestCompositeGeminiNativeCallOfAnotherProvidersModelIsNotFound(t *testing.T) {
	chain := newCompositeGeminiChain(t, nil, service.GroupModelAllowlist{})

	for _, model := range []string{"claude-sonnet-4-5", "gpt-5.4-mini"} {
		w := chain.do(http.MethodPost, "/v1beta/models/"+model+":generateContent", `{"contents":[]}`)
		require.Equal(t, http.StatusNotFound, w.Code, model)
		require.Equal(t, "NOT_FOUND", gjson.Get(w.Body.String(), "error.status").String(), model)
	}
	require.Zero(t, chain.reached)
}

// The group's model allowlist is checked on the name the client wrote, before the composite router is asked.
func TestCompositeGeminiAllowlistBlocksAModelOutsideIt(t *testing.T) {
	chain := newCompositeGeminiChain(t, nil, service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-3.5-flash"}})

	blocked := chain.do(http.MethodPost, "/v1beta/models/gemini-3.1-pro-preview:generateContent", `{"contents":[]}`)
	require.Equal(t, http.StatusNotFound, blocked.Code)
	require.Equal(t, "NOT_FOUND", gjson.Get(blocked.Body.String(), "error.status").String())
	require.Zero(t, chain.reached)

	allowed := chain.do(http.MethodPost, "/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse", `{"contents":[]}`)
	require.Equal(t, http.StatusNoContent, allowed.Code)
	require.Equal(t, 1, chain.reached)
}
