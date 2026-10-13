package service

import (
	"context"
	"strings"
)

// otohaOpenAIMediaPricer reads what the OpenAI gateway charges for a second of video and a unit of voice, with the
// very functions that bill a request, so the catalog's price is the charge (TASK-81).
type otohaOpenAIMediaPricer struct {
	openai *OpenAIGatewayService
}

var _ OtohaMediaPricer = otohaOpenAIMediaPricer{}

// VideoSecondPrice is the price of one second of video of a model at a resolution in the group before the group's
// rate; false when billing does not charge the model per second (a channel that charges it by token or per request)
// or the gateway is not there.
func (p otohaOpenAIMediaPricer) VideoSecondPrice(ctx context.Context, group *Group, modelID, resolution string) (float64, bool) {
	s := p.openai
	if s == nil || s.billingService == nil || group == nil {
		return 0, false
	}
	groupID := group.ID
	apiKey := &APIKey{GroupID: &groupID, Group: group}
	if resolved := s.resolveOpenAIChannelPricing(ctx, modelID, apiKey); resolved != nil {
		if resolved.Mode == BillingModeToken {
			return 0, false
		}
		if resolved.Source == PricingSourceChannel && (resolved.Mode == BillingModePerRequest || resolved.Mode == BillingModeImage) {
			// A channel price per request is charged once whatever the length: there is no price per second.
			return 0, false
		}
	}
	seconds := VideoBillingMinDurationSeconds
	cost := s.calculateOpenAIVideoCost(ctx, modelID, apiKey, &OpenAIForwardResult{
		VideoCount: 1, VideoDurationSeconds: seconds, VideoResolution: resolution,
	}, 1)
	if cost == nil {
		return 0, false
	}
	return cost.TotalCost / float64(seconds), true
}

// VoiceUnitPrice is the price of one unit of a voice function in the group before the group's rate: a million
// characters of text for "tts", an hour of audio for "stt". The usage log names the function as the model.
func (p otohaOpenAIMediaPricer) VoiceUnitPrice(ctx context.Context, group *Group, mode string) (float64, bool) {
	s := p.openai
	if s == nil || s.billingService == nil || group == nil {
		return 0, false
	}
	groupID := group.ID
	apiKey := &APIKey{GroupID: &groupID, Group: group}
	cost, err := s.calculateOpenAIAudioCost(ctx, mode, apiKey, &AudioUsage{Mode: mode, DurationOrUnits: 1}, "", 1)
	if err != nil || cost == nil {
		return 0, false
	}
	return cost.TotalCost, true
}

// MediaRoute tells whether the group can serve a model of a kind on the endpoint the app would call, and whether an
// account serves it there.
//
//   - Voice (POST /v1/tts, /v1/stt): the gateway answers them only for a group whose platform is Grok; a composite
//     group is refused (the handlers check the group's platform, and a voice request names no model to route by).
//   - Video (POST /v1/videos/generations): a Grok group, or a composite group that routes the model to Grok.
//   - Quick judgement (POST /v1/systemone): a TypeSafe group, or a composite group that routes the model to TypeSafe.
func (r otohaDiagnoserRouting) MediaRoute(ctx context.Context, group *Group, kind, modelID string) OtohaModelRoute {
	if group == nil {
		return OtohaModelRoute{}
	}
	want := PlatformGrok
	switch kind {
	case OtohaMediaKindVoice:
		if group.Platform != PlatformGrok {
			return OtohaModelRoute{Problem: OtohaCatalogProblemNoRoute}
		}
		return OtohaModelRoute{}
	case OtohaMediaKindQuick:
		want = PlatformTypeSafe
	}

	route := OtohaModelRoute{}
	model := modelID
	if group.Platform == PlatformComposite {
		target := r.mediaCompositeTarget(ctx, group, modelID)
		if target.failed {
			return OtohaModelRoute{Problem: target.problem, Failed: true}
		}
		if target.platform != want {
			return OtohaModelRoute{Problem: OtohaCatalogProblemNoRoute, Platform: target.platform}
		}
		route.Platform = target.platform
		model = target.model
		if model != modelID {
			route.UpstreamModel = model
		}
	} else if group.Platform != want {
		return OtohaModelRoute{Problem: OtohaCatalogProblemNoRoute}
	}
	routedCtx := ctx
	if route.Platform != "" {
		routedCtx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{
			Matched: true, PublicModel: modelID, TargetPlatform: want, UpstreamModel: model, Endpoint: CompositeRouteEndpointAny,
		})
	}
	if r.restricted != nil && r.restricted(routedCtx, group.ID, model) {
		route.Problem = OtohaCatalogProblemChannelRestricted
		return route
	}
	diagnoser := r.gateway
	if want == PlatformGrok {
		diagnoser = r.openai
	}
	groupID := group.ID
	if diagnoser != nil && !diagnoser.DiagnoseModelAvailabilityForPlatform(ctx, &groupID, model, want).HasModelSupport {
		route.Problem = OtohaCatalogProblemNoAccount
	}
	return route
}

// mediaCompositeTarget routes a model the way the gateway does for the video and System One endpoints: the composite
// resolver decides, and a model it leaves unrouted goes to the provider the model's name belongs to.
func (r otohaDiagnoserRouting) mediaCompositeTarget(ctx context.Context, group *Group, modelID string) otohaCompositeTarget {
	resolver := r.composite
	if resolver == nil {
		resolver = NewCompositeRouteResolver(nil)
	}
	decision, err := resolver.Resolve(ctx, group.ID, modelID, CompositeRouteEndpointAny)
	if err != nil {
		return otohaCompositeTarget{problem: OtohaCatalogProblemNoRoute, failed: true}
	}
	if decision.Matched {
		target := otohaCompositeTarget{platform: decision.TargetPlatform, model: strings.TrimSpace(decision.UpstreamModel)}
		if target.model == "" {
			target.model = modelID
		}
		return target
	}
	if detected, ok := DetectModelPlatform(modelID); ok {
		return otohaCompositeTarget{platform: detected, model: modelID}
	}
	return otohaCompositeTarget{problem: OtohaCatalogProblemNoRoute, platform: decision.TargetPlatform}
}
