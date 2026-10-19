//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestToolsSearchErrorsActualAuth(t *testing.T) {
	for _, tc := range []struct {
		name, key, code, errorType string
		status                     int
	}{
		{"missing", "", "API_KEY_REQUIRED", "authentication_error", 401},
		{"invalid", "invalid-test-key", "INVALID_API_KEY", "authentication_error", 401},
		{"balance", "empty-balance-test-key", "INSUFFICIENT_BALANCE", "insufficient_quota", 402},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
				if key != "empty-balance-test-key" {
					return nil, service.ErrAPIKeyNotFound
				}
				return &service.APIKey{ID: 1, UserID: 1, Key: key, Status: service.StatusActive,
					User: &service.User{ID: 1, Role: service.RoleUser, Status: service.StatusActive}}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			called := false
			auth := gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg))
			router.POST("/v1/tools/search", ToolsSearchErrors(), auth, func(c *gin.Context) { called = true })
			router.POST("/legacy", auth, func(c *gin.Context) { called = true })
			for _, path := range []string{"/v1/tools/search", "/legacy"} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, path, nil)
				if tc.key != "" {
					request.Header.Set("Authorization", "Bearer "+tc.key)
				}
				router.ServeHTTP(response, request)
				require.False(t, called)
				if path == "/legacy" {
					status := tc.status
					if status == 402 {
						status = 403
					}
					require.Equal(t, status, response.Code)
					var body map[string]any
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
					require.Equal(t, tc.code, body["code"])
					require.NotContains(t, body, "error")
					continue
				}
				require.Equal(t, tc.status, response.Code)
				var body struct {
					Error struct {
						Type, Code, Message string
						Param               any
					}
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
				require.Equal(t, tc.code, body.Error.Code)
				require.Equal(t, tc.errorType, body.Error.Type)
				require.NotEmpty(t, body.Error.Message)
				require.Contains(t, response.Body.String(), `"param":null`)
			}
		})
	}
}

func TestToolsSearchErrorsGroupAssignment(t *testing.T) {
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAllowUngroupedKeyScheduling: "false",
	}}, &config.Config{})
	router := gin.New()
	called := false
	router.POST("/v1/tools/search", ToolsSearchErrors(), func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 1})
	}, RequireGroupAssignment(settings, ToolsSearchGroupErrorWriter), func(c *gin.Context) { called = true })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", nil))
	require.Equal(t, http.StatusForbidden, response.Code)
	require.False(t, called)
	require.JSONEq(t, `{"error":{"type":"permission_error","code":"GROUP_REQUIRED","param":null,"message":"API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group."}}`, response.Body.String())
}

func TestToolsSearchErrorsStatusTypesAndAbort(t *testing.T) {
	for _, tc := range []struct {
		status    int
		errorType string
	}{
		{400, "invalid_request_error"}, {403, "permission_error"},
		{429, "rate_limit_error"}, {500, "server_error"}, {503, "server_error"},
	} {
		router := gin.New()
		called := false
		router.POST("/t", ToolsSearchErrors(), func(c *gin.Context) {
			c.Header("Retry-After", "17")
			AbortWithError(c, tc.status, "TEST_CODE", "Safe message")
		}, func(c *gin.Context) { called = true })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/t", nil))
		require.Equal(t, tc.status, response.Code)
		require.False(t, called)
		require.Equal(t, "17", response.Header().Get("Retry-After"))
		want, err := json.Marshal(gin.H{"error": gin.H{"type": tc.errorType, "code": "TEST_CODE", "message": "Safe message", "param": nil}})
		require.NoError(t, err)
		require.JSONEq(t, string(want), response.Body.String())
	}
}

func TestToolsSearchErrorsRateLimitSuppliesRetryAfter(t *testing.T) {
	router := gin.New()
	router.POST("/t", ToolsSearchErrors(), func(c *gin.Context) {
		AbortWithError(c, http.StatusTooManyRequests, "SUBSCRIPTION_LIMIT", "Allowance used")
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/t", nil))
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.Equal(t, "60", response.Header().Get("Retry-After"))
}
