package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newOtohaRoutesTestRouter(redisClient *redis.Client, jwt gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterOtohaRoutes(v1, handler.NewOtohaHandler(nil), servermiddleware.JWTAuthMiddleware(jwt), nil, nil, redisClient)
	return router
}

func postOtohaClaim(router *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	// An empty body fails before the service is touched, so a 400 proves the request passed the limiter.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/otoha/claim", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestOtohaClaimIsRateLimitedPerIP(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	router := newOtohaRoutesTestRouter(rdb, func(c *gin.Context) { c.Next() })

	for i := 1; i <= otohaClaimRateLimit; i++ {
		w := postOtohaClaim(router, "198.51.100.30:1234")
		require.Equal(t, http.StatusBadRequest, w.Code, "request %d should reach the handler", i)
	}
	w := postOtohaClaim(router, "198.51.100.30:1234")
	require.Equal(t, http.StatusTooManyRequests, w.Code)

	w = postOtohaClaim(router, "198.51.100.31:1234")
	require.Equal(t, http.StatusBadRequest, w.Code, "another address has its own budget")
}

func TestOtohaClaimRefusesWhenTheLimiterIsDown(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	router := newOtohaRoutesTestRouter(rdb, func(c *gin.Context) { c.Next() })

	w := postOtohaClaim(router, "198.51.100.32:1234")
	require.Equal(t, http.StatusTooManyRequests, w.Code, "codes cannot be guessed while the limiter is unavailable")
}

func TestOtohaAccountEndpointsSitBehindLogin(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	router := newOtohaRoutesTestRouter(rdb, func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/otoha/claims"},
		{http.MethodGet, "/api/v1/otoha/config"},
		{http.MethodGet, "/api/v1/otoha/account"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, tc.path)
	}
	require.Equal(t, http.StatusBadRequest, postOtohaClaim(router, "198.51.100.33:1234").Code, "the claim exchange needs no login")
}
