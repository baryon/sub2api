package service

import (
	"context"
	"strings"
)

// SetOtohaImagePriceOverrides gives the gateway the prices the admin set on the Otoha catalog's image models, which
// billing then charges (TASK-73). The catalog service itself prices an image through this gateway, so the link is
// made after both exist.
func (s *OpenAIGatewayService) SetOtohaImagePriceOverrides(overrides OtohaImagePriceOverrides) {
	s.otohaImagePrices = overrides
}

// otohaImageOverrideCost charges an image at the price the admin set on the model in the group's Otoha catalog, for
// the size tier the request resolved to; false when the group's catalog sets no price for that model and tier, which
// leaves the charge to billing's own rules. The first billing candidate with a price wins, in the order billing tries
// them (the requested model is among them).
func (s *OpenAIGatewayService) otohaImageOverrideCost(
	ctx context.Context,
	billingModels []string,
	apiKey *APIKey,
	result *OpenAIForwardResult,
	multiplier float64,
) (*CostBreakdown, bool) {
	if s == nil || result == nil {
		return nil, false
	}
	return otohaImageOverrideCostFor(ctx, s.otohaImagePrices, s.billingService, billingModels, apiKey, result.ImageSize, result.ImageCount, multiplier)
}

// otohaImageOverrideCostFor is the charge for imageCount images of a size at the price the admin set on the first of
// the models that has one for the tier.
func otohaImageOverrideCostFor(
	ctx context.Context,
	overrides OtohaImagePriceOverrides,
	billing *BillingService,
	billingModels []string,
	apiKey *APIKey,
	imageSize string,
	imageCount int,
	multiplier float64,
) (*CostBreakdown, bool) {
	if overrides == nil || billing == nil || apiKey == nil || apiKey.GroupID == nil {
		return nil, false
	}
	tier := NormalizeImageBillingTierOrDefault(imageSize)
	for _, candidate := range billingModels {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		prices, ok := overrides.OtohaImagePriceOverrides(ctx, *apiKey.GroupID, candidate)
		if !ok || prices.For(tier) == nil {
			continue
		}
		config := &ImagePriceConfig{Price1K: prices.P1K, Price2K: prices.P2K, Price4K: prices.P4K}
		return billing.CalculateImageCost(candidate, tier, imageCount, config, multiplier), true
	}
	return nil, false
}

// otohaOpenAIImagePricer reads what the OpenAI gateway charges for one image of a model, with the very function that
// bills a request, so the catalog's price is the charge.
type otohaOpenAIImagePricer struct {
	openai *OpenAIGatewayService
}

var _ OtohaImagePricer = otohaOpenAIImagePricer{}

// ImageUnitPrice is the price of one image of a size tier in the group before the group's rate; false when billing
// charges the model by token (a channel or group price in token mode) or the gateway is not there.
func (p otohaOpenAIImagePricer) ImageUnitPrice(ctx context.Context, group *Group, modelID, tier string) (float64, bool) {
	s := p.openai
	if s == nil || s.billingService == nil || group == nil {
		return 0, false
	}
	groupID := group.ID
	apiKey := &APIKey{GroupID: &groupID, Group: group}
	if resolved := s.resolveOpenAIChannelPricing(ctx, modelID, apiKey); resolved != nil && resolved.Mode == BillingModeToken {
		return 0, false
	}
	// A price of the billing candidates' own, not the catalog's: the catalog asks for the price that applies when
	// the admin has set none, so it must not see an override.
	cost := s.calculateOpenAIImageCost(ctx, modelID, apiKey, &OpenAIForwardResult{ImageCount: 1, ImageSize: tier}, 1)
	if cost == nil {
		return 0, false
	}
	return cost.TotalCost, true
}

// SetOtohaImagePriceOverrides gives the Anthropic gateway, which bills the Gemini picture models, the prices the
// admin set on the catalog's image models (TASK-85).
func (s *GatewayService) SetOtohaImagePriceOverrides(overrides OtohaImagePriceOverrides) {
	s.otohaImagePrices = overrides
}

// otohaGeminiImagePricer reads what the gateway charges for one picture of a Gemini model, with the very function
// that bills a request, so the catalog's price is the charge (TASK-85).
type otohaGeminiImagePricer struct {
	gateway *GatewayService
}

var _ OtohaImagePricer = otohaGeminiImagePricer{}

// ImageUnitPrice is the price of one picture of a size tier in the group before the group's rate; false when billing
// charges the model by token (a channel or group price in token mode), has no price of its own for the model (an
// unknown name would be charged billing's built-in default), or the gateway is not there.
func (p otohaGeminiImagePricer) ImageUnitPrice(ctx context.Context, group *Group, modelID, tier string) (float64, bool) {
	s := p.gateway
	if s == nil || s.billingService == nil || group == nil {
		return 0, false
	}
	groupID := group.ID
	apiKey := &APIKey{GroupID: &groupID, Group: group}
	resolved := s.resolveChannelPricing(ctx, modelID, apiKey)
	if resolved != nil && resolved.Mode == BillingModeToken {
		return 0, false
	}
	if resolved == nil && !apiKeyHasConfiguredImagePrice(apiKey, tier) && !s.billingService.HasOwnImagePrice(modelID) {
		return 0, false
	}
	// A price of billing's own, not the catalog's: the catalog asks for the price that applies when the admin has set
	// none, so it must not see an override.
	cost := s.calculateImageCost(ctx, &ForwardResult{ImageCount: 1, ImageSize: tier}, apiKey, modelID, 1)
	if cost == nil {
		return 0, false
	}
	return cost.TotalCost, true
}

// otohaImagePricerByPlatform prices a picture with the gateway that bills it: the Anthropic gateway for a Gemini
// model, the OpenAI gateway for the others. The platform is the one the route resolved, else the group's own.
type otohaImagePricerByPlatform struct {
	openai OtohaImagePricer
	gemini OtohaImagePricer
}

var _ OtohaImagePricer = otohaImagePricerByPlatform{}

func (p otohaImagePricerByPlatform) ImageUnitPrice(ctx context.Context, group *Group, modelID, tier string) (float64, bool) {
	platform, ok := ResolvedTargetPlatformFromContext(ctx)
	if !ok && group != nil {
		platform = group.Platform
	}
	if platform == PlatformGemini {
		return p.gemini.ImageUnitPrice(ctx, group, modelID, tier)
	}
	return p.openai.ImageUnitPrice(ctx, group, modelID, tier)
}
