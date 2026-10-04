package service

import (
	"context"
	"encoding/json"
	"strings"
)

// NewOtohaCatalogService wires the Otoha catalog to the price resolver billing uses, the Codex manifest metadata and
// the gateways' model availability checks.
func NewOtohaCatalogService(
	repo OtohaCatalogRepository,
	groupRepo GroupRepository,
	resolver *ModelPricingResolver,
	gatewayService *GatewayService,
	openAIGatewayService *OpenAIGatewayService,
	accountRepo AccountRepository,
) *OtohaCatalogService {
	routing := otohaDiagnoserRouting{}
	if gatewayService != nil {
		routing.gateway = gatewayService
	}
	if openAIGatewayService != nil {
		routing.openai = openAIGatewayService
	}
	return NewOtohaCatalogServiceWithDeps(
		repo,
		groupRepo,
		otohaResolverPricer{resolver: resolver},
		otohaGatewayMetadata{gateway: gatewayService, accounts: accountRepo},
		routing,
	)
}

// otohaResolverPricer reads the token price billing resolves for the model in the group (group price, then channel
// price, then the price table), at its standard rate: time-of-day, long-context, priority-tier and reasoning
// multipliers are not part of the catalog price.
type otohaResolverPricer struct {
	resolver *ModelPricingResolver
}

func (p otohaResolverPricer) UpstreamPrice(ctx context.Context, group *Group, modelID string) (OtohaModelPrice, bool) {
	if p.resolver == nil || group == nil {
		return OtohaModelPrice{}, false
	}
	groupID := group.ID
	resolved := p.resolver.Resolve(ctx, PricingInput{Model: modelID, GroupID: &groupID, Group: group})
	if resolved == nil || resolved.BasePricing == nil {
		return OtohaModelPrice{}, false
	}
	if resolved.Mode != "" && resolved.Mode != BillingModeToken {
		return OtohaModelPrice{}, false
	}
	base := resolved.BasePricing
	if base.InputPricePerToken <= 0 && base.OutputPricePerToken <= 0 {
		return OtohaModelPrice{}, false
	}
	price := OtohaModelPrice{
		Input:  base.InputPricePerToken * 1e6,
		Output: base.OutputPricePerToken * 1e6,
	}
	if base.CacheReadPricePerToken > 0 {
		cached := base.CacheReadPricePerToken * 1e6
		price.CachedInput = &cached
	}
	return price, true
}

// otohaGatewayMetadata reads a model's metadata from the Codex manifest the group would serve (display name,
// inputs, context, reasoning levels), and fills gaps from the metadata synced onto the group's accounts.
type otohaGatewayMetadata struct {
	gateway  *GatewayService
	accounts AccountRepository
}

func (m otohaGatewayMetadata) ModelMetadata(ctx context.Context, group *Group, modelID string) (OtohaModelMetadata, error) {
	var md OtohaModelMetadata
	found := false
	if m.gateway != nil && group != nil {
		if body, err := m.gateway.BuildCodexModelsManifestForGroup(ctx, group, "", []string{modelID}); err == nil {
			md, found = otohaMetadataFromCodexManifest(body, modelID)
		}
	}
	if m.accounts != nil && group != nil {
		if accounts, err := m.accounts.ListByGroup(ctx, group.ID); err == nil {
			var synced []UpstreamModelMetadata
			for i := range accounts {
				if meta, ok := accounts[i].GetUpstreamModelMetadata(modelID); ok {
					synced = append(synced, meta)
				}
			}
			if len(synced) > 0 {
				md = mergeOtohaAccountMetadata(md, synced)
				found = true
			}
		}
	}
	if !found {
		return OtohaModelMetadata{}, ErrOtohaCatalogEntryNotFound
	}
	return md, nil
}

type otohaCodexManifestModel struct {
	Slug                     string `json:"slug"`
	DisplayName              string `json:"display_name"`
	Description              string `json:"description"`
	DefaultReasoningLevel    string `json:"default_reasoning_level"`
	SupportedReasoningLevels []struct {
		Effort string `json:"effort"`
	} `json:"supported_reasoning_levels"`
	InputModalities []string `json:"input_modalities"`
	ContextWindow   int64    `json:"context_window"`
}

// otohaMetadataFromCodexManifest takes one model's public metadata from a Codex manifest. Codex lists only models
// that call tools; the gateway's own placeholder wording is not a model description.
func otohaMetadataFromCodexManifest(body []byte, modelID string) (OtohaModelMetadata, bool) {
	var manifest struct {
		Models []otohaCodexManifestModel `json:"models"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return OtohaModelMetadata{}, false
	}
	for _, model := range manifest.Models {
		if model.Slug != modelID {
			continue
		}
		md := OtohaModelMetadata{
			Name:    strings.TrimSpace(model.DisplayName),
			Inputs:  model.InputModalities,
			Tools:   true,
			Context: int(model.ContextWindow),
		}
		if md.Context == configuredCodexFallbackContext && !isOpenAICodexGPTModel(modelID) {
			// The manifest's stand-in for a model whose context it does not know.
			md.Context = 0
		}
		if description := strings.TrimSpace(model.Description); !strings.Contains(strings.ToLower(description), "sub2api") {
			md.Description = description
		}
		for _, level := range model.SupportedReasoningLevels {
			if effort := strings.TrimSpace(level.Effort); effort != "" && effort != "none" {
				md.Reasoning = append(md.Reasoning, effort)
			}
		}
		if len(md.Reasoning) > 0 && otohaContains(md.Reasoning, model.DefaultReasoningLevel) {
			md.DefaultReasoning = model.DefaultReasoningLevel
		}
		return md, true
	}
	return OtohaModelMetadata{}, false
}

// mergeOtohaAccountMetadata fills what the manifest left empty from metadata synced onto accounts (an upstream
// /models response or models.dev); the longest reply is the largest any account reports.
func mergeOtohaAccountMetadata(md OtohaModelMetadata, synced []UpstreamModelMetadata) OtohaModelMetadata {
	for _, meta := range synced {
		if md.Name == "" {
			md.Name = strings.TrimSpace(meta.DisplayName)
		}
		if md.Description == "" {
			md.Description = strings.TrimSpace(meta.Description)
		}
		if len(md.Inputs) == 0 && len(meta.InputModalities) > 0 {
			md.Inputs = append([]string(nil), meta.InputModalities...)
		}
		if md.Context == 0 && meta.ContextWindow > 0 {
			md.Context = int(meta.ContextWindow)
		}
		if int(meta.MaxOutputTokens) > md.MaxOutput {
			md.MaxOutput = int(meta.MaxOutputTokens)
		}
		if len(md.Reasoning) == 0 && len(meta.SupportedReasoningLevels) > 0 {
			for _, level := range meta.SupportedReasoningLevels {
				if level != "none" {
					md.Reasoning = append(md.Reasoning, level)
				}
			}
			if otohaContains(md.Reasoning, meta.DefaultReasoningLevel) {
				md.DefaultReasoning = meta.DefaultReasoningLevel
			}
		}
	}
	return md
}

// otohaDiagnoserRouting asks the gateway that serves the group's platform whether an account is configured for the
// model, the same check that turns "no account" into model_not_found. Transient state (rate limits, overload) does
// not hide a model. A composite group decides per request, so only its allowlist applies.
type otohaDiagnoserRouting struct {
	gateway ModelAvailabilityDiagnoser
	openai  ModelAvailabilityDiagnoser
}

func (r otohaDiagnoserRouting) CanRoute(ctx context.Context, group *Group, modelID string) bool {
	if group == nil || group.Platform == PlatformComposite {
		return true
	}
	groupID := group.ID
	var diagnoser ModelAvailabilityDiagnoser
	switch group.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
		diagnoser = r.openai
	default:
		diagnoser = r.gateway
	}
	if diagnoser == nil {
		return true
	}
	return diagnoser.DiagnoseModelAvailabilityForPlatform(ctx, &groupID, modelID, group.Platform).HasModelSupport
}
