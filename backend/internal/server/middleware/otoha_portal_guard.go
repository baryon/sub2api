package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OtohaPortalUserGuard stops regular users with err while the site runs as the Otoha portal (TASK-61); admins and
// sites without the portal pass. It runs after JWT auth; a request without a role counts as a regular user.
func OtohaPortalUserGuard(enabled bool, err error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled {
			c.Next()
			return
		}
		if role, _ := GetUserRoleFromContext(c); role == service.RoleAdmin {
			c.Next()
			return
		}
		response.ErrorFrom(c, err)
		c.Abort()
	}
}
