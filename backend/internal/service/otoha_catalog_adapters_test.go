//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func otohaTestResolver() *ModelPricingResolver {
	return NewModelPricingResolver(nil, NewBillingService(nil, nil))
}

func TestOtohaUpstreamPriceUsesTheGroupsOwnPrice(t *testing.T) {
	pricer := otohaResolverPricer{resolver: otohaTestResolver()}
	group := &Group{ID: 3, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{{
		Models: []string{"gpt-6-luna"}, BillingMode: BillingModeToken,
		InputPrice: otohaFloat(2e-6), OutputPrice: otohaFloat(8e-6), CacheReadPrice: otohaFloat(2e-7),
	}}}

	price, ok := pricer.UpstreamPrice(context.Background(), group, "gpt-6-luna")
	require.True(t, ok)
	require.InDelta(t, 2.0, price.Input, 1e-9)
	require.InDelta(t, 8.0, price.Output, 1e-9)
	require.NotNil(t, price.CachedInput)
	require.InDelta(t, 0.2, *price.CachedInput, 1e-9)
}

func TestOtohaUpstreamPriceFallsBackToThePriceTable(t *testing.T) {
	pricer := otohaResolverPricer{resolver: otohaTestResolver()}
	billing := NewBillingService(nil, nil)
	table, err := billing.GetModelPricing("claude-sonnet-4-5")
	require.NoError(t, err)

	price, ok := pricer.UpstreamPrice(context.Background(), &Group{ID: 3, Platform: PlatformAnthropic}, "claude-sonnet-4-5")
	require.True(t, ok)
	require.InDelta(t, table.InputPricePerToken*1e6, price.Input, 1e-9)
	require.InDelta(t, table.OutputPricePerToken*1e6, price.Output, 1e-9)
}

func TestOtohaUpstreamPriceOfAModelPricedPerRequestIsNone(t *testing.T) {
	pricer := otohaResolverPricer{resolver: otohaTestResolver()}
	group := &Group{ID: 3, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{{
		Models: []string{"image-model"}, BillingMode: BillingModePerRequest, PerRequestPrice: otohaFloat(0.04),
	}}}
	_, ok := pricer.UpstreamPrice(context.Background(), group, "image-model")
	require.False(t, ok, "a per-request price is not a token price")

	_, ok = otohaResolverPricer{}.UpstreamPrice(context.Background(), group, "gpt-6-luna")
	require.False(t, ok)
}

func TestOtohaMetadataFromTheCodexManifest(t *testing.T) {
	body := []byte(`{"models":[{"slug":"gpt-6-luna","display_name":"GPT-6 Luna","description":"Fast frontier model",
		"default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}],
		"input_modalities":["text","image"],"context_window":1050000,"max_context_window":1050000,
		"model_messages":{"instructions_template":"secret"}}]}`)
	md, ok := otohaMetadataFromCodexManifest(body, "gpt-6-luna")
	require.True(t, ok)
	require.Equal(t, OtohaModelMetadata{
		Name: "GPT-6 Luna", Description: "Fast frontier model", Inputs: []string{"text", "image"}, Tools: true,
		Context: 1050000, Reasoning: []string{"low", "medium", "high"}, DefaultReasoning: "medium",
	}, md)

	_, ok = otohaMetadataFromCodexManifest(body, "other")
	require.False(t, ok)
	_, ok = otohaMetadataFromCodexManifest([]byte(`not json`), "gpt-6-luna")
	require.False(t, ok)
}

func TestOtohaMetadataLeavesOutGatewayWordingAndNoReasoning(t *testing.T) {
	body := []byte(`{"models":[{"slug":"mystery","display_name":"mystery","description":"Custom model routed through Sub2API.",
		"default_reasoning_level":"none","supported_reasoning_levels":[{"effort":"none"}],"input_modalities":["text"],"context_window":128000}]}`)
	md, ok := otohaMetadataFromCodexManifest(body, "mystery")
	require.True(t, ok)
	require.Empty(t, md.Description, "the gateway's own wording is not a model description")
	require.Empty(t, md.Reasoning, "\"none\" alone means no reasoning levels")
	require.Empty(t, md.DefaultReasoning)
}

func TestOtohaMetadataFillsGapsFromSyncedAccountMetadata(t *testing.T) {
	md := mergeOtohaAccountMetadata(OtohaModelMetadata{Name: "GPT-6 Luna", Context: 1050000}, []UpstreamModelMetadata{
		{ID: "gpt-6-luna", MaxOutputTokens: 64000, Description: "from models.dev"},
		{ID: "gpt-6-luna", MaxOutputTokens: 128000, ContextWindow: 400000},
	})
	require.Equal(t, "GPT-6 Luna", md.Name)
	require.Equal(t, "from models.dev", md.Description)
	require.Equal(t, 1050000, md.Context, "the manifest's context wins")
	require.Equal(t, 128000, md.MaxOutput)
}

type fakeOtohaDiagnoser struct {
	calls     []string
	supported bool
}

func (d *fakeOtohaDiagnoser) DiagnoseModelAvailabilityForPlatform(_ context.Context, groupID *int64, model, platform string) ModelAvailabilityDiagnosis {
	d.calls = append(d.calls, platform+":"+model)
	return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: d.supported}
}

func TestOtohaRoutingAsksTheGatewayThatServesTheGroup(t *testing.T) {
	gateway := &fakeOtohaDiagnoser{supported: true}
	openai := &fakeOtohaDiagnoser{supported: false}
	routing := otohaDiagnoserRouting{gateway: gateway, openai: openai}
	ctx := context.Background()

	require.Equal(t, OtohaCatalogProblemNoAccount, routing.Route(ctx, &Group{ID: 1, Platform: PlatformOpenAI}, "gpt-6-luna").Problem)
	require.Equal(t, OtohaCatalogProblemNoAccount, routing.Route(ctx, &Group{ID: 1, Platform: PlatformDeepseek}, "deepseek-v4").Problem)
	require.Equal(t, OtohaModelRoute{}, routing.Route(ctx, &Group{ID: 1, Platform: PlatformAnthropic}, "claude-opus"),
		"a group of one provider serves the model as itself")
	require.Equal(t, []string{"openai:gpt-6-luna", "deepseek:deepseek-v4"}, openai.calls)
	require.Equal(t, []string{"anthropic:claude-opus"}, gateway.calls)

	require.Equal(t, OtohaModelRoute{Platform: PlatformAnthropic}, routing.Route(ctx, &Group{ID: 1, Platform: PlatformComposite}, "claude-sonnet-4-5"),
		"without routes a composite group goes by the model name, and asks that provider's gateway")
	require.Equal(t, "anthropic:claude-sonnet-4-5", gateway.calls[len(gateway.calls)-1])
	require.Equal(t, OtohaModelRoute{}, otohaDiagnoserRouting{}.Route(ctx, &Group{ID: 1, Platform: PlatformOpenAI}, "x"),
		"without a gateway the check does not hide models")
}

// The manifest gives unknown models a generic context; the catalog leaves it out rather than state a wrong one.
func TestOtohaMetadataDropsTheManifestsGenericContext(t *testing.T) {
	manifest := func(slug string) []byte {
		return []byte(`{"models":[{"slug":"` + slug + `","display_name":"x","input_modalities":["text"],"context_window":272000}]}`)
	}
	md, ok := otohaMetadataFromCodexManifest(manifest("claude-opus-4-1"), "claude-opus-4-1")
	require.True(t, ok)
	require.Zero(t, md.Context, "272000 is the manifest's stand-in for an unknown context")

	md, ok = otohaMetadataFromCodexManifest(manifest("gpt-5.4"), "gpt-5.4")
	require.True(t, ok)
	require.Equal(t, 272000, md.Context, "GPT models do have this context")
}

// The OpenAI gateway bills a request as the model the serving account maps it to; the catalog asks for those names.
func TestOtohaBilledModelsAreTheServingAccountsMappings(t *testing.T) {
	groupID := int64(9)
	account := func(id int64, mapping map[string]any) Account {
		return Account{ID: id, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
			AccountGroups: []AccountGroup{{GroupID: groupID}}, Credentials: map[string]any{"model_mapping": mapping}}
	}
	repo := &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{}, accounts: []Account{
		account(1, map[string]any{"gpt-6-luna": "gpt-6-luna-2026"}),
		account(2, map[string]any{"gpt-6-luna": "gpt-6-luna-2026"}),
		account(3, map[string]any{"gpt-6-luna": "gpt-6-luna-pro"}),
		account(4, map[string]any{"other": "other"}),
	}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: testConfig()}

	billed := func(svc *OpenAIGatewayService, model, claimedBy string) []string {
		names, err := svc.OtohaBilledModels(context.Background(), &groupID, model, PlatformOpenAI, claimedBy)
		require.NoError(t, err)
		return names
	}
	require.Equal(t, []string{"gpt-6-luna-2026", "gpt-6-luna-pro"}, billed(svc, "gpt-6-luna", ""))
	require.Equal(t, []string{"other"}, billed(svc, "other", ""))
	require.Empty(t, billed(svc, "unserved", ""))
	require.Empty(t, billed(nil, "gpt-6-luna", ""))

	repo.accounts = append(repo.accounts, Account{ID: 5, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
		AccountGroups: []AccountGroup{{GroupID: groupID}}})
	require.Equal(t, []string{"gpt-6-luna", "gpt-6-luna-2026", "gpt-6-luna-pro"}, billed(svc, "gpt-6-luna", ""),
		"an account without a mapping serves every model under its own name")
	require.Equal(t, []string{"gpt-6-luna-2026", "gpt-6-luna-pro"}, billed(svc, "gpt-6-luna", "gpt-6-luna"),
		"a model routed by account ownership is served only by accounts whose mapping names it")
}
