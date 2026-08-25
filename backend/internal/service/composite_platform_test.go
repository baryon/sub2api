package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type compositeOwnershipAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *compositeOwnershipAccountRepo) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

func TestResolveCompositeModelOwnershipKeepsProviderAccountsIsolated(t *testing.T) {
	groupID := int64(7)
	repo := &compositeOwnershipAccountRepo{
		accounts: []Account{
			{
				ID:       1,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"gpt-public": "gpt-5"},
				},
			},
			{
				ID:       2,
				Platform: PlatformDeepSeek,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"reasoning-alias": "deepseek-v4-pro"},
				},
			},
		},
	}
	svc := &GatewayService{accountRepo: repo}

	deepSeekOwnership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "reasoning-alias")
	require.NoError(t, err)
	require.True(t, deepSeekOwnership.Matched)
	require.Equal(t, PlatformDeepSeek, deepSeekOwnership.TargetPlatform)

	openAIOwnership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "gpt-public")
	require.NoError(t, err)
	require.True(t, openAIOwnership.Matched)
	require.Equal(t, PlatformOpenAI, openAIOwnership.TargetPlatform)
}

func TestResolveCompositeModelOwnershipIgnoresWildcardsAndEmptyMappings(t *testing.T) {
	groupID := int64(7)
	repo := &compositeOwnershipAccountRepo{
		accounts: []Account{
			{
				ID:       1,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"*": "gpt-5", "gpt-*": "gpt-5"},
				},
			},
			{
				ID:          2,
				Platform:    PlatformDeepSeek,
				Credentials: map[string]any{"api_key": "sk-deepseek"},
			},
			{
				ID:       3,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"grok-public": "grok-4"},
				},
			},
		},
	}
	svc := &GatewayService{accountRepo: repo}

	wildcardOwnership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "deepseek-v4-pro")
	require.NoError(t, err)
	require.False(t, wildcardOwnership.Matched)
	require.False(t, wildcardOwnership.Ambiguous)
	require.Empty(t, wildcardOwnership.TargetPlatform)

	gptOwnership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "gpt-5")
	require.NoError(t, err)
	require.False(t, gptOwnership.Matched)

	grokOwnership, err := svc.resolveCompositeModelOwnership(context.Background(), groupID, "grok-public")
	require.NoError(t, err)
	require.True(t, grokOwnership.Matched)
	require.Equal(t, PlatformGrok, grokOwnership.TargetPlatform)
}

func TestDetectModelPlatform(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		platform string
		ok       bool
	}{
		{name: "claude", model: "claude-sonnet-4-5", platform: PlatformAnthropic, ok: true},
		{name: "anthropic prefix", model: "anthropic/claude-opus-4-5", platform: PlatformAnthropic, ok: true},
		{name: "gpt", model: "gpt-5.1", platform: PlatformOpenAI, ok: true},
		{name: "o series", model: "o3-mini", platform: PlatformOpenAI, ok: true},
		{name: "embedding", model: "text-embedding-3-large", platform: PlatformOpenAI, ok: true},
		{name: "gemini", model: "gemini-3-pro", platform: PlatformGemini, ok: true},
		{name: "gemini models prefix", model: "models/gemini-2.5-flash", platform: PlatformGemini, ok: true},
		{name: "learnlm", model: "learnlm-2.0-flash-experimental", platform: PlatformGemini, ok: true},
		{name: "grok", model: "grok-4", platform: PlatformGrok, ok: true},
		{name: "xai prefix", model: "xai/grok-4", platform: PlatformGrok, ok: true},
		{name: "kimi", model: "kimi-k2", platform: PlatformKimi, ok: true},
		{name: "kimi exact", model: "kimi", platform: PlatformKimi, ok: true},
		{name: "kimi thinking", model: "kimi-k2-thinking", platform: PlatformKimi, ok: true},
		{name: "kimi code bare k3", model: "K3", platform: PlatformKimi, ok: true},
		{name: "kimi code bare k3 256k", model: "k3-256k", platform: PlatformKimi, ok: true},
		{name: "kimi code provider prefix", model: "kimi-code/k3", platform: PlatformKimi, ok: true},
		{name: "moonshot prefix", model: "moonshot/kimi-k2", platform: PlatformKimi, ok: true},
		{name: "moonshot model prefix", model: "moonshot/moonshot-v1-32k", platform: PlatformKimi, ok: true},
		{name: "glm", model: "glm-4.6", platform: PlatformZhipu, ok: true},
		{name: "zhipu latest", model: "glm-5.2", platform: PlatformZhipu, ok: true},
		{name: "zhipu prefix", model: "zhipu/glm-4.5", platform: PlatformZhipu, ok: true},
		{name: "deepseek", model: "deepseek-v4-pro", platform: PlatformDeepSeek, ok: true},
		{name: "deepseek prefix", model: "deepseek/deepseek-v4-flash", platform: PlatformDeepSeek, ok: true},
		{name: "unknown k3 alias", model: "k3-preview", ok: false},
		{name: "unknown", model: "llama-4-maverick", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, ok := DetectModelPlatform(tt.model)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.platform, platform)
		})
	}
}

func TestQuotaPlatformCompositeUsesResolvedOrForceOnly(t *testing.T) {
	apiKey := &APIKey{Group: &Group{Platform: PlatformComposite}}

	require.Equal(t, "", QuotaPlatform(context.Background(), apiKey))
	require.Equal(t, PlatformGemini, QuotaPlatform(WithResolvedTargetPlatform(context.Background(), PlatformGemini), apiKey))
	require.Equal(t, PlatformAntigravity, QuotaPlatform(context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAntigravity), apiKey))

	ctx := WithResolvedTargetPlatform(context.Background(), PlatformAnthropic)
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, PlatformAntigravity)
	require.Equal(t, PlatformAntigravity, QuotaPlatform(ctx, apiKey))
}

func TestCompositeGroupSchedulerHasAllCanonicalPlatformBuckets(t *testing.T) {
	seen := make(map[string]struct{})
	for _, bucket := range schedulerCanonicalBuckets(99) {
		seen[bucket.Platform] = struct{}{}
	}
	platforms := make([]string, 0, len(seen))
	for platform := range seen {
		platforms = append(platforms, platform)
	}
	require.ElementsMatch(t,
		[]string{
			PlatformAnthropic,
			PlatformGemini,
			PlatformOpenAI,
			PlatformAntigravity,
			PlatformGrok,
			PlatformDeepSeek,
			PlatformKimi,
			PlatformZhipu,
		},
		platforms,
	)
}

func TestResolveCompositeRouteDecisionExplicitRouteOverridesDetectorContext(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformComposite}
	routes := []CompositeModelRoute{
		{ID: 1, GroupID: group.ID, PublicModel: "gpt-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-flash", Endpoint: CompositeRouteEndpointChatCompletions, Priority: 100, Enabled: true},
		{ID: 2, GroupID: group.ID, PublicModel: "grok-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: CompositeRouteEndpointResponses, Priority: 100, Enabled: true},
		{ID: 3, GroupID: group.ID, PublicModel: "claude-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: CompositeRouteEndpointMessages, Priority: 100, Enabled: true},
	}
	svc := &GatewayService{compositeResolver: NewCompositeRouteResolver(compositeRouteRepoStub{routes: routes})}

	for _, route := range routes {
		t.Run(route.Endpoint, func(t *testing.T) {
			detectedPlatform, ok := DetectModelPlatform(route.PublicModel)
			require.True(t, ok)
			require.NotEqual(t, PlatformDeepSeek, detectedPlatform)
			ctx := WithResolvedTargetPlatform(context.Background(), detectedPlatform)
			ctx = WithCompositeRouteEndpoint(ctx, route.Endpoint)

			decision, matched, err := svc.resolveCompositeRouteDecision(ctx, group, route.PublicModel, CompositeRouteEndpointAny)

			require.NoError(t, err)
			require.True(t, matched)
			require.Equal(t, CompositeRouteSourceExplicit, decision.Source)
			require.Equal(t, PlatformDeepSeek, decision.TargetPlatform)
			require.Equal(t, route.UpstreamModel, decision.UpstreamModel)
			require.Equal(t, route.Endpoint, decision.Endpoint)
		})
	}
}

func TestCompositeConcretePlatformsIncludeCNProviders(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepSeek} {
		require.True(t, isConcreteRequestPlatform(platform))
		require.True(t, canCopyAccountsFromGroupPlatform(PlatformComposite, platform))
	}
}
