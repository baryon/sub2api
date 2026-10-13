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
	if s == nil || s.otohaImagePrices == nil || s.billingService == nil || apiKey == nil || apiKey.GroupID == nil || result == nil {
		return nil, false
	}
	tier := NormalizeImageBillingTierOrDefault(result.ImageSize)
	for _, candidate := range billingModels {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		prices, ok := s.otohaImagePrices.OtohaImagePriceOverrides(ctx, *apiKey.GroupID, candidate)
		if !ok || prices.For(tier) == nil {
			continue
		}
		config := &ImagePriceConfig{Price1K: prices.P1K, Price2K: prices.P2K, Price4K: prices.P4K}
		return s.billingService.CalculateImageCost(candidate, tier, result.ImageCount, config, multiplier), true
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
