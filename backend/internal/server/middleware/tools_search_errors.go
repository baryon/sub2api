package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const toolsSearchErrorsContextKey = "sub2api.tools_search.errors"

// ToolsSearchErrors opts a route into OpenAI-compatible middleware errors.
// Install before API key authentication.
func ToolsSearchErrors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(toolsSearchErrorsContextKey, true)
		c.Next()
	}
}

// ToolsSearchGroupErrorWriter supplies the tools search envelope to
// RequireGroupAssignment, which owns aborting the handler chain.
func ToolsSearchGroupErrorWriter(c *gin.Context, status int, message string) {
	writeToolsSearchMiddlewareError(c, status, "GROUP_REQUIRED", message)
}

func writeToolsSearchMiddlewareError(c *gin.Context, status int, code, message string) {
	if code == "INSUFFICIENT_BALANCE" {
		status = http.StatusPaymentRequired
	}
	if status == http.StatusTooManyRequests && c.Writer.Header().Get("Retry-After") == "" {
		c.Header("Retry-After", "60")
	}
	errorType := "invalid_request_error"
	switch {
	case status == http.StatusUnauthorized:
		errorType = "authentication_error"
	case status == http.StatusPaymentRequired:
		errorType = "insufficient_quota"
	case status == http.StatusForbidden:
		errorType = "permission_error"
	case status == http.StatusTooManyRequests:
		errorType = "rate_limit_error"
	case status >= http.StatusInternalServerError:
		errorType = "server_error"
	}
	c.JSON(status, gin.H{"error": gin.H{
		"message": message,
		"type":    errorType,
		"param":   nil,
		"code":    code,
	}})
}
