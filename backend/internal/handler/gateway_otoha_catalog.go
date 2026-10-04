package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OtohaClientQueryValue marks requests from the Otoha app: `GET /v1/models?client=otoha` gives the group's Otoha
// catalog and `GET /v1/usage?client=otoha` adds this period's usage per model (TASK-54).
const OtohaClientQueryValue = "otoha"

const otohaUsageExtraContextKey = "otoha_usage_extra"

// IsOtohaClientRequest reports whether the request asks for the Otoha app's shapes.
func IsOtohaClientRequest(c *gin.Context) bool {
	return c != nil && c.Request != nil && strings.EqualFold(strings.TrimSpace(c.Query("client")), OtohaClientQueryValue)
}

// SetOtohaCatalog gives the handler the Otoha catalog reader.
func (h *GatewayHandler) SetOtohaCatalog(reader service.OtohaCatalogReader) {
	h.otohaCatalog = reader
}

// OtohaModels returns the Otoha catalog of the key's group; a group without one gets the plain model list.
// GET /v1/models?client=otoha
func (h *GatewayHandler) OtohaModels(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || h.otohaCatalog == nil {
		h.Models(c)
		return
	}
	catalog, err := h.readOtohaCatalog(c, apiKey)
	if err != nil {
		logger.L().Warn("otoha_catalog.read_failed", zap.Int64("group_id", apiKey.Group.ID), zap.Error(err))
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The model list is temporarily unavailable. Please retry later.")
		return
	}
	if catalog == nil {
		h.Models(c)
		return
	}
	etag := `"` + catalog.Revision + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	if service.CodexModelsManifestETagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.JSON(http.StatusOK, catalog)
}

// readOtohaCatalog prices the group's catalog at the rate the key's owner pays in the group (a rate of their own
// replaces the group's in billing), so the app shows what this user is charged.
func (h *GatewayHandler) readOtohaCatalog(c *gin.Context, apiKey *service.APIKey) (*service.OtohaCatalog, error) {
	ctx := c.Request.Context()
	groupID := apiKey.Group.ID
	rateReader, priced := h.otohaCatalog.(service.OtohaCatalogRateReader)
	if !priced || apiKey.GroupID == nil {
		return h.otohaCatalog.CatalogForGroup(ctx, groupID)
	}
	rate, ok := h.resolveKeyBillingRate(c, apiKey)
	if !ok {
		return h.otohaCatalog.CatalogForGroup(ctx, groupID)
	}
	return rateReader.CatalogForGroupAtRate(ctx, groupID, rate)
}

// prepareOtohaUsage reads this period's usage per model through the key for the Otoha app; the usage writers add it
// to their response. Best effort: a failed read leaves it out.
func (h *GatewayHandler) prepareOtohaUsage(c *gin.Context, apiKey *service.APIKey) {
	if !IsOtohaClientRequest(c) || apiKey == nil || h.usageService == nil {
		return
	}
	var plan *service.UserSubscription
	if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() {
		plan, _ = middleware2.GetSubscriptionFromContext(c)
	}
	now := timezone.Now()
	start, end := service.OtohaUsagePeriod(plan, now)
	stats, err := h.usageService.GetAPIKeyModelStats(c.Request.Context(), apiKey.ID, start, now)
	if err != nil {
		logger.L().Warn("otoha_usage.model_stats_failed", zap.Int64("api_key_id", apiKey.ID), zap.Error(err))
		return
	}
	c.Set(otohaUsageExtraContextKey, gin.H{
		"period": gin.H{
			"start": start.Format(time.RFC3339),
			"end":   end.Format(time.RFC3339),
		},
		"model_usage": service.OtohaModelUsageFromStats(stats),
	})
}

// addOtohaUsage copies what prepareOtohaUsage read into a /v1/usage response.
func addOtohaUsage(c *gin.Context, resp gin.H) {
	value, ok := c.Get(otohaUsageExtraContextKey)
	if !ok {
		return
	}
	extra, ok := value.(gin.H)
	if !ok {
		return
	}
	for k, v := range extra {
		resp[k] = v
	}
}
