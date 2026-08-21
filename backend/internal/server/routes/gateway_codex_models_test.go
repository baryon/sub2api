package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesCodexModelsManifestPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	registered := make(map[string]string)
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet {
			registered[route.Path] = route.Handler
		}
	}

	require.NotEmpty(t, registered["/backend-api/codex/models"], "GET /backend-api/codex/models should be registered")
	require.NotEmpty(t, registered["/v1/models"], "GET /v1/models should be registered")
	require.NotEmpty(t, registered["/models"], "GET /models should be registered")
	require.Equal(t, registered["/v1/models"], registered["/models"], "root alias should use the same platform-aware handler")
}

func TestGatewayRoutesCompositeCodexModelsUsesLocalCatalog(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformComposite)

	for _, path := range []string{
		"/v1/models?client_version=0.147.0",
		"/models?client_version=0.147.0",
		"/backend-api/codex/models?client_version=0.147.0",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), `"models"`, "path=%s", path)
		require.NotContains(t, w.Body.String(), "No available OpenAI accounts", "path=%s", path)
		require.NotContains(t, w.Body.String(), "only available for OpenAI", "path=%s", path)
	}
}
