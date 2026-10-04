//go:build unit

package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newOtohaPortalGuardRouter(enabled bool, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if role != "" {
			c.Set(string(ContextKeyUserRole), role)
		}
		c.Next()
	})
	r.POST("/keys", OtohaPortalUserGuard(enabled, service.ErrOtohaPortalKeysManaged), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestOtohaPortalUserGuard(t *testing.T) {
	cases := []struct {
		name       string
		enabled    bool
		role       string
		wantStatus int
	}{
		{name: "portal off lets users through", enabled: false, role: service.RoleUser, wantStatus: http.StatusOK},
		{name: "portal on stops users", enabled: true, role: service.RoleUser, wantStatus: http.StatusForbidden},
		{name: "portal on lets admins through", enabled: true, role: service.RoleAdmin, wantStatus: http.StatusOK},
		{name: "portal on stops requests without a role", enabled: true, role: "", wantStatus: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newOtohaPortalGuardRouter(tc.enabled, tc.role).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/keys", nil))
			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantStatus == http.StatusForbidden {
				var body struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				require.Equal(t, "OTOHA_PORTAL_KEYS_MANAGED", body.Reason)
				require.NotEmpty(t, body.Message)
			}
		})
	}
}
