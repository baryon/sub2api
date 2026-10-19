package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func toolsSearchRateRequest(limiter gin.HandlerFunc, identity gin.HandlerFunc) (*httptest.ResponseRecorder, bool) {
	called := false
	router := gin.New()
	router.Use(gin.Recovery())
	router.POST("/v1/tools/search", identity, limiter, func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/tools/search", nil))
	return recorder, called
}

func toolsSearchRateIdentity(userID, keyID, groupID int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: keyID, UserID: userID, GroupID: &groupID})
	}
}

func TestToolsSearchRateLimitUserBuckets(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := ToolsSearchRateLimit(client)
	for i := 0; i < 60; i++ {
		response, called := toolsSearchRateRequest(limiter, toolsSearchRateIdentity(1, int64(i+1), int64(i+1)))
		require.Equal(t, http.StatusNoContent, response.Code)
		require.True(t, called)
	}
	// Separate middleware instances must still share the same user's budget.
	response, called := toolsSearchRateRequest(ToolsSearchRateLimit(client), toolsSearchRateIdentity(1, 999, 999))
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.False(t, called)
	require.Equal(t, "60", response.Header().Get("Retry-After"))
	require.JSONEq(t, `{"error":{"message":"Too many search requests. Please try again later.","type":"rate_limit_error","param":null,"code":"rate_limit_exceeded"}}`, response.Body.String())
	response, called = toolsSearchRateRequest(limiter, toolsSearchRateIdentity(2, 1000, 999))
	require.Equal(t, http.StatusNoContent, response.Code)
	require.True(t, called)
	server.FastForward(59500 * time.Millisecond)
	response, called = toolsSearchRateRequest(limiter, toolsSearchRateIdentity(1, 999, 999))
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.Equal(t, "1", response.Header().Get("Retry-After"))
	require.False(t, called)
	server.FastForward(500 * time.Millisecond)
	response, called = toolsSearchRateRequest(limiter, toolsSearchRateIdentity(1, 999, 999))
	require.Equal(t, http.StatusNoContent, response.Code)
	require.True(t, called)
}

func TestToolsSearchRateLimitFailsClosed(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	server.SetError("ERR unavailable")
	for name, redisClient := range map[string]*redis.Client{"missing": nil, "unavailable": client} {
		t.Run(name, func(t *testing.T) {
			// Recovery makes a panic observable as an incorrect 500 rather than a 503.
			response, called := toolsSearchRateRequest(ToolsSearchRateLimit(redisClient), toolsSearchRateIdentity(1, 1, 1))
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.False(t, called)
			require.JSONEq(t, `{"error":{"message":"Search is temporarily unavailable. Please try again later.","type":"server_error","param":null,"code":"service_unavailable"}}`, response.Body.String())
		})
	}
}

func TestToolsSearchRateLimitUnauthorized(t *testing.T) {
	identities := map[string]gin.HandlerFunc{
		"missing":           func(c *gin.Context) {},
		"nil key":           func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), (*service.APIKey)(nil)) },
		"wrong type":        func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), "invalid") },
		"zero user":         toolsSearchRateIdentity(0, 1, 1),
		"negative user":     toolsSearchRateIdentity(-1, 1, 1),
		"invalid subject":   func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: -1}) },
		"ops fallback only": func(c *gin.Context) { SetOpsFallbackAPIKey(c, &service.APIKey{UserID: 1}) },
	}
	for name, identity := range identities {
		t.Run(name, func(t *testing.T) {
			response, called := toolsSearchRateRequest(ToolsSearchRateLimit(nil), identity)
			require.Equal(t, http.StatusUnauthorized, response.Code)
			require.False(t, called)
			require.JSONEq(t, `{"error":{"message":"Authentication is required.","type":"authentication_error","param":null,"code":"invalid_api_key"}}`, response.Body.String())
		})
	}
}

func TestToolsSearchRateLimitSubjectAndAdminShareUserBudget(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := ToolsSearchRateLimit(client)
	identity := func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
		c.Set(string(ContextKeyUserRole), service.RoleAdmin)
	}
	for i := 0; i < 60; i++ {
		response, called := toolsSearchRateRequest(limiter, identity)
		require.Equal(t, http.StatusNoContent, response.Code)
		require.True(t, called)
	}
	response, called := toolsSearchRateRequest(limiter, identity)
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.False(t, called)
	response, called = toolsSearchRateRequest(limiter, toolsSearchRateIdentity(1, 10, 20))
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.False(t, called)
}

func TestToolsSearchRateLimitStandaloneDoesNotAdvanceChain(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := ToolsSearchRateLimit(client)
	var events []string
	router := gin.New()
	router.POST("/v1/tools/search", toolsSearchRateIdentity(1, 1, 1), func(c *gin.Context) {
		limiter(c)
		if c.IsAborted() {
			return
		}
		events = append(events, "billing")
	}, func(c *gin.Context) { events = append(events, "next handler") })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/tools/search", nil))
	require.Equal(t, []string{"billing", "next handler"}, events)
}
