package routes

import (
	"encoding/json"
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

// newUserRoutesPortalTestRouter registers the real user routes. Handlers have no services behind them, so the
// requests below are ones the handlers reject (400) before touching a service: a 400 proves the request got past
// the portal guard, a 403 with the portal reason proves the guard stopped it.
func newUserRoutesPortalTestRouter(portal bool, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Otoha.GroupID = 2
	cfg.Otoha.GatewayBaseURL = "https://api.example.com"
	cfg.Otoha.Portal = portal
	settings := service.NewSettingService(&channelMonitorRouteSettingRepoStub{values: map[string]string{}}, cfg)

	jwt := func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7})
		c.Set(string(servermiddleware.ContextKeyUserRole), role)
		c.Next()
	}
	passthrough := func(c *gin.Context) { c.Next() }

	// Recovery turns a handler without a service behind it into a 500, which also proves the guard let it pass.
	router := gin.New()
	router.Use(gin.Recovery())
	v1 := router.Group("/api/v1")
	h := &handler.Handlers{APIKey: handler.NewAPIKeyHandler(nil)}
	RegisterUserRoutes(v1, h, servermiddleware.JWTAuthMiddleware(jwt), servermiddleware.AuditLogMiddleware(passthrough), settings, nil)
	RegisterModelPlazaRoutes(v1, h, servermiddleware.OptionalJWTAuthMiddleware(jwt), settings, nil)
	return router
}

func serveUserRoute(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func responseReason(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Reason
}

// optionalReason is the error reason of a JSON reply, or "" for any other reply (e.g. a recovered panic).
func optionalReason(w *httptest.ResponseRecorder) string {
	var body struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		return ""
	}
	return body.Reason
}

// Requests that change keys; each is malformed so an unguarded handler answers 400 without a service.
var keyChangingRequests = []struct{ method, path, body string }{
	{http.MethodPost, "/api/v1/keys", `{}`},
	{http.MethodPut, "/api/v1/keys/not-a-number", `{}`},
	{http.MethodDelete, "/api/v1/keys/not-a-number", ``},
}

func TestUserRoutes_OtohaPortalStopsUsersChangingKeys(t *testing.T) {
	router := newUserRoutesPortalTestRouter(true, service.RoleUser)
	for _, tc := range keyChangingRequests {
		w := serveUserRoute(router, tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusForbidden, w.Code, "%s %s", tc.method, tc.path)
		require.Equal(t, "OTOHA_PORTAL_KEYS_MANAGED", responseReason(t, w), "%s %s", tc.method, tc.path)
	}
}

// Endpoints that list groups, channels or rates. Their handlers have no service here, so a request that gets past
// the guard ends in some other error (a recovered 500, or the handler's own refusal).
var groupListingPaths = []string{
	"/api/v1/groups/available",
	"/api/v1/groups/rates",
	"/api/v1/groups/3/models",
	"/api/v1/channels/available",
	"/api/v1/model-plaza",
}

func TestUserRoutes_OtohaPortalStopsUsersPickingGroups(t *testing.T) {
	router := newUserRoutesPortalTestRouter(true, service.RoleUser)
	for _, path := range groupListingPaths {
		w := serveUserRoute(router, http.MethodGet, path, "")
		require.Equal(t, http.StatusForbidden, w.Code, path)
		require.Equal(t, "OTOHA_PORTAL_GROUPS_MANAGED", responseReason(t, w), path)
	}
}

func TestUserRoutes_OtohaPortalStillLetsUsersReadTheirKeys(t *testing.T) {
	router := newUserRoutesPortalTestRouter(true, service.RoleUser)
	w := serveUserRoute(router, http.MethodGet, "/api/v1/keys/not-a-number", "")
	require.Equal(t, http.StatusBadRequest, w.Code, "reading a key is not blocked")
}

func TestUserRoutes_OtohaPortalLeavesAdminsAlone(t *testing.T) {
	router := newUserRoutesPortalTestRouter(true, service.RoleAdmin)
	for _, tc := range keyChangingRequests {
		w := serveUserRoute(router, tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusBadRequest, w.Code, "%s %s should reach the handler", tc.method, tc.path)
	}
}

func TestUserRoutes_WithoutTheOtohaPortalNothingChanges(t *testing.T) {
	router := newUserRoutesPortalTestRouter(false, service.RoleUser)
	for _, tc := range keyChangingRequests {
		w := serveUserRoute(router, tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusBadRequest, w.Code, "%s %s should reach the handler", tc.method, tc.path)
	}
}

func TestUserRoutes_OtohaPortalLetsAdminsListGroups(t *testing.T) {
	router := newUserRoutesPortalTestRouter(true, service.RoleAdmin)
	for _, path := range groupListingPaths {
		w := serveUserRoute(router, http.MethodGet, path, "")
		require.NotEqual(t, "OTOHA_PORTAL_GROUPS_MANAGED", optionalReason(w), "%s should reach the handler", path)
	}
}

func TestUserRoutes_WithoutTheOtohaPortalGroupsStayListed(t *testing.T) {
	router := newUserRoutesPortalTestRouter(false, service.RoleUser)
	for _, path := range groupListingPaths {
		w := serveUserRoute(router, http.MethodGet, path, "")
		require.NotEqual(t, "OTOHA_PORTAL_GROUPS_MANAGED", optionalReason(w), "%s should reach the handler", path)
	}
}
