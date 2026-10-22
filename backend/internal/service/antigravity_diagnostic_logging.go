package service

import (
	"bufio"
	"context"
	"errors"
	"mime"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

const antigravityDiagnosticAccountKey = "agy_diagnostic_account_id"

func antigravityDiagnosticCorrelation(c *gin.Context) []any {
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	return []any{"request_id", requestID, "account_id", c.GetInt64(antigravityDiagnosticAccountKey)}
}

// Only fixed labels are logged: upstream headers and errors can contain private
// request data, so never serialize their original strings for diagnostics.
func antigravityDiagnosticContentType(value string) string {
	if value == "" {
		return "missing"
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "other"
	}
	switch mediaType {
	case "text/event-stream":
		return "sse"
	case "application/json":
		return "json"
	default:
		return "other"
	}
}

func antigravityDiagnosticError(err error) string {
	var repetitionErr *antigravityStreamRepetitionError
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, bufio.ErrTooLong):
		return "line_too_long"
	case strings.Contains(err.Error(), "pre-content stream timeout"):
		return "pre_content_timeout"
	case strings.Contains(err.Error(), "stream data interval timeout"):
		return "stream_interval_timeout"
	case strings.Contains(err.Error(), "empty Antigravity"):
		return "empty_stream"
	case errors.As(err, &repetitionErr):
		return "repetition_loop"
	default:
		return "other"
	}
}
