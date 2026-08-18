package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type userGroupClientModelsResponse struct {
	Models []string `json:"models"`
}

// ListUserGroupModels returns the model IDs a bindable group will expose to
// Codex and Claude clients. The list matches GET /v1/models for that group.
func (h *GatewayHandler) ListUserGroupModels(c *gin.Context) {
	if h == nil || h.apiKeyService == nil {
		response.ErrorFrom(c, service.ErrGroupNotAllowed)
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	group, err := h.apiKeyService.GetBindableGroupForUser(c.Request.Context(), subject.UserID, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	models := listClientVisibleModelIDs(c.Request.Context(), h.gatewayService, group, "")
	if models == nil {
		models = []string{}
	}
	response.Success(c, userGroupClientModelsResponse{Models: models})
}

func listClientVisibleModelIDs(
	ctx context.Context,
	gateway *service.GatewayService,
	group *service.Group,
	platformOverride string,
) []string {
	if group == nil {
		return nil
	}
	platform := strings.TrimSpace(platformOverride)
	if platform == "" {
		platform = group.Platform
	}
	groupID := group.ID

	var modelIDs []string
	if platform == service.PlatformComposite {
		modelIDs = listCompositeAvailableModels(ctx, gateway, &groupID)
		if group.CustomModelsListEnabled() {
			modelIDs = filterModelsByCustomList(modelIDs, nil, group.ModelsListConfig.Models)
		}
	} else {
		if gateway != nil {
			modelIDs = gateway.GetAvailableModels(ctx, &groupID, platform)
		}
		fallbackModels := []string(nil)
		if gateway != nil {
			if _, available := gateway.GetSchedulablePlatforms(ctx, &groupID)[platform]; available {
				fallbackModels = defaultModelIDsForPlatform(platform)
			}
		}
		if group.CustomModelsListEnabled() {
			source := customModelsListSource(platform, modelIDs, fallbackModels)
			modelIDs = filterModelsByCustomList(source, fallbackModels, group.ModelsListConfig.Models)
		} else if len(modelIDs) == 0 {
			modelIDs = fallbackModels
		}
	}
	return mergeModelIDs(modelIDs, nil)
}

func listCompositeAvailableModels(ctx context.Context, gateway *service.GatewayService, groupID *int64) []string {
	if gateway == nil {
		return nil
	}
	seen := make(map[string]struct{})
	models := make([]string, 0)
	schedulablePlatforms := gateway.GetSchedulablePlatforms(ctx, groupID)
	for _, platform := range []string{
		service.PlatformAnthropic,
		service.PlatformGemini,
		service.PlatformOpenAI,
		service.PlatformAntigravity,
		service.PlatformGrok,
		service.PlatformDeepSeek,
		service.PlatformKimi,
		service.PlatformZhipu,
	} {
		platformModels := gateway.GetAvailableModels(ctx, groupID, platform)
		if len(platformModels) == 0 {
			if _, ok := schedulablePlatforms[platform]; ok {
				platformModels = defaultModelIDsForPlatform(platform)
			}
		}
		for _, model := range platformModels {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			models = append(models, model)
		}
	}
	return models
}
