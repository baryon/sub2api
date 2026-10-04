package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	ratelimit "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// otohaClaimRateLimit caps claim-code exchanges per client IP. The app exchanges one code per purchase, so this
// leaves room for retries while making guessing pointless; the limiter refuses when Redis is unavailable.
const (
	otohaClaimRateLimit       = 10
	otohaClaimRateLimitWindow = time.Minute
)

// RegisterOtohaRoutes registers the Otoha purchase-page endpoints and the app's claim exchange (TASK-55).
func RegisterOtohaRoutes(
	v1 *gin.RouterGroup,
	h *handler.OtohaHandler,
	jwtAuth middleware.JWTAuthMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
	redisClient *redis.Client,
) {
	authenticated := v1.Group("/otoha")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	authenticated.Use(panelRateLimiter.Global())
	{
		authenticated.POST("/claims", h.CreateClaim)
		authenticated.GET("/config", h.DownloadConfig)
		authenticated.GET("/account", h.GetAccount)
	}

	limiter := ratelimit.NewRateLimiter(redisClient)
	v1.POST("/otoha/claim",
		limiter.LimitWithOptions("otoha-claim", otohaClaimRateLimit, otohaClaimRateLimitWindow,
			ratelimit.RateLimitOptions{FailureMode: ratelimit.RateLimitFailClose}),
		h.RedeemClaim)
}
