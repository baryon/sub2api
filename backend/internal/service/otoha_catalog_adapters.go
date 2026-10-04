package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
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
		// The same resolver the request middleware uses, with the account ownership lookup connected.
		if gatewayService.compositeResolver != nil {
			routing.composite = gatewayService.compositeResolver
		}
	}
	if openAIGatewayService != nil {
		routing.openai = openAIGatewayService
		routing.openaiBilling = openAIGatewayService
	}
	if gatewayService != nil && gatewayService.channelService != nil {
		routing.restricted = func(ctx context.Context, groupID int64, model string) bool {
			return gatewayService.checkChannelPricingRestriction(ctx, &groupID, model)
		}
		routing.channelMapping = gatewayService.channelService.ResolveChannelMapping
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
			// A composite group's accounts belong to several providers; only those of the provider the model is
			// routed to describe it, under the name it is forwarded as.
			platform, lookupModel := "", modelID
			if group.Platform == PlatformComposite {
				platform, _ = ResolvedTargetPlatformFromContext(ctx)
				if upstream, ok := ResolvedUpstreamModelFromContext(ctx); ok {
					lookupModel = upstream
				}
			}
			if synced := otohaSyncedAccountMetadata(accounts, platform, lookupModel); len(synced) > 0 {
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

// otohaSyncedAccountMetadata gathers the metadata synced onto the accounts (of the provider, when given, with the
// Antigravity accounts scheduled alongside it) for a model.
func otohaSyncedAccountMetadata(accounts []Account, platform, modelID string) []UpstreamModelMetadata {
	var synced []UpstreamModelMetadata
	for i := range accounts {
		mixedIn := (platform == PlatformAnthropic || platform == PlatformGemini) && accounts[i].IsMixedSchedulingEnabled()
		if platform != "" && accounts[i].Platform != platform && !mixedIn {
			continue
		}
		if meta, ok := accounts[i].GetUpstreamModelMetadata(modelID); ok {
			synced = append(synced, meta)
		}
	}
	return synced
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

// otohaDiagnoserRouting tells where the group sends the app's requests for a model, the way the gateway does on the
// endpoint of the format the app calls the model in (TASK-64: Claude on /v1/messages, the others on /v1/responses),
// and asks the gateway that serves that provider whether an account is configured for the model (the check that
// turns "no account" into model_not_found; transient state such as rate limits does not hide a model). A composite
// group resolves the provider per model: an explicit route, else the provider whose accounts claim the model, else
// the provider its name belongs to.
type otohaDiagnoserRouting struct {
	gateway   ModelAvailabilityDiagnoser
	openai    ModelAvailabilityDiagnoser
	composite otohaCompositeResolver
	// openaiBilling tells what the OpenAI gateway's accounts bill a model as.
	openaiBilling otohaOpenAIBilledModels
	// restricted is the gateway's check that the group's channel refuses a model it does not price.
	restricted func(ctx context.Context, groupID int64, model string) bool
	// channelMapping is the group's channel model mapping, which the gateways apply before forwarding.
	channelMapping func(ctx context.Context, groupID int64, model string) ChannelMappingResult
}

// otohaOpenAIBilledModels gives the names the accounts serving a model bill it as (OpenAIGatewayService).
type otohaOpenAIBilledModels interface {
	OtohaBilledModels(ctx context.Context, groupID *int64, model, platform, claimedBy string) ([]string, error)
}

// otohaCompositeResolver is the composite router the gateway's request middleware uses.
type otohaCompositeResolver interface {
	Resolve(ctx context.Context, groupID int64, model, endpoint string) (CompositeRouteDecision, error)
}

// otohaCompositeTarget is where a composite group sends a model on one endpoint.
type otohaCompositeTarget struct {
	platform string
	// model is the name the provider receives.
	model string
	owned bool
	// problem is OtohaCatalogProblemNoRoute when the endpoint cannot tell; failed when the lookup failed.
	problem string
	failed  bool
}

// resolveComposite routes a model on one endpoint as the gateway's request middleware and handlers do: the composite
// resolver decides; a model it leaves unrouted reaches the endpoint's Anthropic-gateway handler, which goes by the
// model's name (Messages serves Claude, Antigravity and Gemini models that way; Responses Claude and Antigravity ones).
func (r otohaDiagnoserRouting) resolveComposite(ctx context.Context, group *Group, modelID, endpoint string) otohaCompositeTarget {
	resolver := r.composite
	if resolver == nil {
		// The gateway's middleware falls back to the model-name detector alone the same way.
		resolver = NewCompositeRouteResolver(nil)
	}
	decision, err := resolver.Resolve(ctx, group.ID, modelID, endpoint)
	if err != nil {
		// The request middleware answers 500 then; the model is left out of this read only.
		logger.L().Warn("otoha_catalog.composite_route_failed", zap.Int64("group_id", group.ID), zap.String("model", modelID), zap.String("endpoint", endpoint), zap.Error(err))
		return otohaCompositeTarget{problem: OtohaCatalogProblemNoRoute, failed: true}
	}
	if !decision.Matched {
		detected, ok := DetectModelPlatform(modelID)
		servedByName := ok && (detected == PlatformAnthropic || detected == PlatformAntigravity ||
			(endpoint == CompositeRouteEndpointMessages && detected == PlatformGemini))
		if !servedByName {
			return otohaCompositeTarget{problem: OtohaCatalogProblemNoRoute, platform: decision.TargetPlatform}
		}
		decision = CompositeRouteDecision{Matched: true, TargetPlatform: detected, UpstreamModel: modelID}
	}
	target := otohaCompositeTarget{
		platform: decision.TargetPlatform,
		model:    strings.TrimSpace(decision.UpstreamModel),
		owned:    decision.Source == CompositeRouteSourceAccount,
	}
	if target.model == "" {
		target.model = modelID
	}
	return target
}

func (r otohaDiagnoserRouting) Route(ctx context.Context, group *Group, modelID, api string) OtohaModelRoute {
	if group == nil {
		return OtohaModelRoute{}
	}
	route := OtohaModelRoute{}
	platform, model := group.Platform, modelID
	ownedRoute := false
	if group.Platform == PlatformComposite {
		var target otohaCompositeTarget
		if api != "" {
			target = r.resolveComposite(ctx, group, modelID, otohaAPIEndpoint(api))
		} else {
			// The app calls a Claude model on /v1/messages and any other on /v1/responses: the model is a Claude one
			// when /v1/messages sends it to a provider whose format is Anthropic Messages.
			target = r.resolveComposite(ctx, group, modelID, CompositeRouteEndpointMessages)
			if target.failed {
				return OtohaModelRoute{Problem: target.problem, Failed: true}
			}
			if target.problem == "" && OtohaNativeAPIForPlatform(target.platform) == OtohaAPIAnthropicMessages {
				api = OtohaAPIAnthropicMessages
			} else {
				target = r.resolveComposite(ctx, group, modelID, CompositeRouteEndpointResponses)
				if target.problem == "" {
					api = OtohaNativeAPIForPlatform(target.platform)
					if api == OtohaAPIAnthropicMessages {
						// /v1/responses sends it to Claude, but /v1/messages, which the app would call, does not.
						return OtohaModelRoute{Problem: OtohaCatalogProblemNoRoute, Platform: target.platform}
					}
				}
			}
		}
		if target.problem != "" {
			return OtohaModelRoute{Problem: target.problem, Platform: target.platform, Failed: target.failed}
		}
		platform, model, ownedRoute = target.platform, target.model, target.owned
		route.Platform = platform
		if model != modelID {
			route.UpstreamModel = model
		}
	} else if api == "" {
		api = OtohaNativeAPIForPlatform(platform)
	}
	if api == "" {
		route.Problem = OtohaCatalogProblemNoNativeAPI
		return route
	}
	route.API = api
	if !otohaAPIReachesPlatform(api, platform) {
		route.Problem = OtohaCatalogProblemAPIUnreachable
		return route
	}
	routedCtx := route.routedContext(ctx, modelID)
	if r.restricted != nil && r.restricted(routedCtx, group.ID, model) {
		route.Problem = OtohaCatalogProblemChannelRestricted
		return route
	}
	// The gateways forward the model under the name the group's channel maps it to, and by default bill that name.
	served := model
	var channelBilled []string
	if r.channelMapping != nil {
		if mapping := r.channelMapping(routedCtx, group.ID, model); mapping.Mapped && strings.TrimSpace(mapping.MappedModel) != "" {
			served = strings.TrimSpace(mapping.MappedModel)
			switch mapping.BillingModelSource {
			case BillingModelSourceRequested:
				channelBilled = []string{modelID}
			case BillingModelSourceUpstream:
				// The serving account's mapping decides, below.
			default:
				channelBilled = []string{served}
			}
		}
	}
	diagnoser := r.gateway
	if otohaOpenAIGatewayPlatform(platform) {
		diagnoser = r.openai
	}
	groupID := group.ID
	if diagnoser != nil && !diagnoser.DiagnoseModelAvailabilityForPlatform(ctx, &groupID, served, platform).HasModelSupport {
		route.Problem = OtohaCatalogProblemNoAccount
		return route
	}
	billed := channelBilled
	switch {
	case billed != nil:
	case otohaOpenAIGatewayPlatform(platform) && r.openaiBilling != nil:
		// The OpenAI gateway bills what the serving account maps the model to; a model routed by account
		// ownership is served only by the accounts that claim it.
		claimedBy := ""
		if ownedRoute {
			claimedBy = modelID
		}
		names, err := r.openaiBilling.OtohaBilledModels(ctx, &groupID, served, platform, claimedBy)
		if err != nil {
			logger.L().Warn("otoha_catalog.billed_models_failed", zap.Int64("group_id", group.ID), zap.String("model", modelID), zap.Error(err))
			route.Failed = true
		}
		billed = names
	default:
		// The Anthropic gateway bills the model as it forwards it.
		billed = []string{served}
	}
	if len(billed) != 1 || billed[0] != model {
		route.BilledModels = billed
	}
	return route
}

// otohaOpenAIGatewayPlatform is a provider whose requests the OpenAI gateway handles.
func otohaOpenAIGatewayPlatform(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
		return true
	default:
		return false
	}
}

// otohaAPIReachesPlatform tells whether the app, calling a model in a format, reaches a provider's accounts and gets
// that format back. Anthropic Messages (`/v1/messages`): the Anthropic gateway serves Anthropic and Antigravity
// accounts natively and converts Messages for Gemini accounts, and the OpenAI gateway bridges Messages for the
// OpenAI-compatible providers. DeepSeek Responses (`/v1/responses`): only DeepSeek accounts answer in DeepSeek's own
// dialect, which the gateway passes through. OpenAI Responses (`/v1/responses`): the OpenAI gateway serves the other
// OpenAI-compatible providers, and the Anthropic gateway converts Responses for Anthropic and Antigravity accounts; a
// DeepSeek account answers in DeepSeek's dialect (reasoning items without the summary OpenAI parsers require), and a
// Gemini account would be sent an Anthropic request. TypeSafe takes only its own protocol.
func otohaAPIReachesPlatform(api, platform string) bool {
	switch api {
	case OtohaAPIAnthropicMessages:
		return otohaOpenAIGatewayPlatform(platform) || platform == PlatformAnthropic || platform == PlatformAntigravity ||
			platform == PlatformGemini
	case OtohaAPIDeepSeekResponses:
		return platform == PlatformDeepseek
	case OtohaAPIOpenAIResponses:
		return platform != PlatformDeepseek &&
			(otohaOpenAIGatewayPlatform(platform) || platform == PlatformAnthropic || platform == PlatformAntigravity)
	default:
		return false
	}
}
