package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// ToolsSearchRateLimit limits authenticated search requests across API keys and groups.
// Invoke after authentication and before billing; it aborts on failure and never
// advances the Gin handler chain, so it also supports direct handler invocation.
func ToolsSearchRateLimit(redisClient *redis.Client) gin.HandlerFunc {
	limiter := middleware.NewRateLimiter(redisClient)
	return func(c *gin.Context) {
		var userID int64
		if apiKey, ok := GetAPIKeyFromContext(c); ok && apiKey != nil {
			userID = apiKey.UserID
		} else if subject, ok := GetAuthSubjectFromContext(c); ok {
			userID = subject.UserID
		}
		if userID <= 0 {
			abortToolsSearchRateLimit(c, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Authentication is required.")
			return
		}
		if redisClient == nil {
			abortToolsSearchRateLimit(c, http.StatusServiceUnavailable, "server_error", "service_unavailable", "Search is temporarily unavailable. Please try again later.")
			return
		}
		result, err := limiter.Allow(c.Request.Context(), "tools_search:user:"+strconv.FormatInt(userID, 10), 60, time.Minute)
		if err != nil {
			abortToolsSearchRateLimit(c, http.StatusServiceUnavailable, "server_error", "service_unavailable", "Search is temporarily unavailable. Please try again later.")
			return
		}
		if !result.Allowed {
			seconds := int64(result.RetryAfter / time.Second)
			if result.RetryAfter%time.Second > 0 {
				seconds++
			}
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.FormatInt(seconds, 10))
			abortToolsSearchRateLimit(c, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", "Too many search requests. Please try again later.")
			return
		}
	}
}

func abortToolsSearchRateLimit(c *gin.Context, status int, errorType, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"message": message,
		"type":    errorType,
		"param":   nil,
		"code":    code,
	}})
}
