package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestToolsSearchRouteAcceptsEveryGroupPlatform(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGemini, service.PlatformGrok, service.PlatformDeepSeek, service.PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			router := newGatewayRoutesTestRouter(platform)
			request := httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":""}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Contains(t, response.Body.String(), "query")
		})
	}
}

func TestToolsSearchOversizedBodyReturns413(t *testing.T) {
	for _, body := range []string{`{"query":"` + strings.Repeat("x", 90) + `"}`, `{"query":"q"}` + strings.Repeat(" ", 100)} {
		router := newGatewayRoutesTestRouterWithConfig(&config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1000, TextMaxBodySize: 32}})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(body)))
		require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
		require.Contains(t, response.Body.String(), `"error"`)
	}
}

func TestToolsSearchRouteFormatsAuthenticationErrors(t *testing.T) {
	for _, tc := range []struct {
		code         string
		status, want int
	}{
		{"INVALID_API_KEY", 401, 401}, {"INSUFFICIENT_BALANCE", 403, 402},
	} {
		t.Run(tc.code, func(t *testing.T) {
			router := gin.New()
			RegisterGatewayRoutes(router, &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}},
				middleware.APIKeyAuthMiddleware(func(c *gin.Context) { middleware.AbortWithError(c, tc.status, tc.code, "request rejected") }),
				nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 1024}})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":"q"}`)))
			require.Equal(t, tc.want, response.Code)
			require.Contains(t, response.Body.String(), `"error":`)
			require.Contains(t, response.Body.String(), `"param":null`)
		})
	}
}
