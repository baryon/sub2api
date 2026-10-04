//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Only reading the Otoha catalog skips the credit check; nothing else that mentions it does.
func TestIsOtohaCatalogReadIsOnlyTheCatalogRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		method, target string
		want           bool
	}{
		{http.MethodGet, "/v1/models?client=otoha", true},
		{http.MethodGet, "/models?client=Otoha", true},
		{http.MethodGet, "/v1/models", false},
		{http.MethodGet, "/v1/models?client=codex", false},
		{http.MethodGet, "/v1/models/gpt-6-luna?client=otoha", false},
		{http.MethodPost, "/v1/models?client=otoha", false},
		{http.MethodPost, "/v1/responses?client=otoha", false},
		{http.MethodGet, "/v1/responses?client=otoha", false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(tc.method, tc.target, nil)
		require.Equal(t, tc.want, isOtohaCatalogRead(c), "%s %s", tc.method, tc.target)
	}
}
