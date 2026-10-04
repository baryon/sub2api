//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TASK-62: one composite (mixed) group offers several providers' models. The app only calls /v1/responses, so a
// model is in its catalog when the group routes it, for that endpoint, to a provider that serves the Responses API
// and has an account for it; its price is what billing charges once the request is routed to that provider.

const otohaCompositeGroupID int64 = 70

// fakePlatformDiagnoser answers for the accounts of each provider in the group: platform → models an account serves,
// and model → the names the OpenAI gateway's accounts bill it as.
type fakePlatformDiagnoser struct {
	models map[string][]string
	billed    map[string][]string
	billedErr error
	claimedBy []string
	calls     []string
}

func (d *fakePlatformDiagnoser) OtohaBilledModels(_ context.Context, _ *int64, model, _, claimedBy string) ([]string, error) {
	d.claimedBy = append(d.claimedBy, claimedBy)
	if d.billedErr != nil {
		return nil, d.billedErr
	}
	return d.billed[model], nil
}

func (d *fakePlatformDiagnoser) DiagnoseModelAvailabilityForPlatform(_ context.Context, groupID *int64, model, platform string) ModelAvailabilityDiagnosis {
	d.calls = append(d.calls, platform+":"+model)
	served := d.models[platform]
	diag := ModelAvailabilityDiagnosis{HasAccountsInPool: len(served) > 0}
	for _, m := range served {
		if m == model {
			diag.HasModelSupport = true
		}
	}
	return diag
}

// recordingOtohaMetadata answers like fakeOtohaMetadata and remembers which provider and model each lookup was for.
type recordingOtohaMetadata struct {
	byModel map[string]OtohaModelMetadata
	seen    []string
}

func (m *recordingOtohaMetadata) ModelMetadata(ctx context.Context, _ *Group, modelID string) (OtohaModelMetadata, error) {
	platform, _ := ResolvedTargetPlatformFromContext(ctx)
	upstream, _ := ResolvedUpstreamModelFromContext(ctx)
	m.seen = append(m.seen, platform+":"+upstream)
	md, ok := m.byModel[modelID]
	if !ok {
		return OtohaModelMetadata{}, ErrOtohaCatalogEntryNotFound
	}
	return md, nil
}

type otohaCompositeFixture struct {
	group     *Group
	routes    []CompositeModelRoute
	ownership map[string]CompositeModelOwnership
	resolveErr error
	restricted map[string]bool
	channelMap map[string]ChannelMappingResult
	diagnoser *fakePlatformDiagnoser
	metadata  *recordingOtohaMetadata
	repo      *fakeOtohaCatalogRepo
	svc       *OtohaCatalogService
}

// newOtohaCompositeFixture builds the catalog over the real composite resolver and the real price resolver: the
// group's channel prices openai and anthropic models per provider, and carries a wrong anthropic price for the GPT
// model, which only a lookup that ignores the provider would pick.
func newOtohaCompositeFixture(t *testing.T) *otohaCompositeFixture {
	t.Helper()
	f := &otohaCompositeFixture{
		group:     &Group{ID: otohaCompositeGroupID, Name: "Otoha", Platform: PlatformComposite, RateMultiplier: 2},
		ownership:  map[string]CompositeModelOwnership{},
		restricted: map[string]bool{},
		channelMap: map[string]ChannelMappingResult{},
		diagnoser: &fakePlatformDiagnoser{billed: map[string][]string{}, models: map[string][]string{
			PlatformOpenAI:    {"gpt-6-luna"},
			PlatformAnthropic: {"claude-sonnet-4-5"},
		}},
		metadata: &recordingOtohaMetadata{byModel: map[string]OtohaModelMetadata{}},
		repo:     newFakeOtohaCatalogRepo(),
	}
	channel := Channel{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{otohaCompositeGroupID},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"gpt-6-luna"}, InputPrice: otohaFloat(100e-6), OutputPrice: otohaFloat(100e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-6-luna"}, InputPrice: otohaFloat(1e-6), OutputPrice: otohaFloat(4e-6)},
			{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4-5"}, InputPrice: otohaFloat(3e-6), OutputPrice: otohaFloat(15e-6), CacheReadPrice: otohaFloat(0.3e-6)},
			{Platform: PlatformAnthropic, Models: []string{"claude-opus-4-1"}, InputPrice: otohaFloat(15e-6), OutputPrice: otohaFloat(75e-6)},
			{Platform: PlatformGemini, Models: []string{"gemini-2.5-pro"}, InputPrice: otohaFloat(1.25e-6), OutputPrice: otohaFloat(10e-6)},
			{Platform: PlatformOpenAI, Models: []string{"house-model"}, InputPrice: otohaFloat(0.5e-6), OutputPrice: otohaFloat(1e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-6-luna-2026"}, InputPrice: otohaFloat(1.5e-6), OutputPrice: otohaFloat(6e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-6-luna-pro"}, InputPrice: otohaFloat(5e-6), OutputPrice: otohaFloat(20e-6)},
		},
	}
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{otohaCompositeGroupID: PlatformComposite}))

	resolver := NewCompositeRouteResolver(otohaRoutesFunc(func() ([]CompositeModelRoute, error) { return f.routes, f.resolveErr }))
	resolver.SetModelOwnershipResolver(func(_ context.Context, groupID int64, model string) (CompositeModelOwnership, error) {
		return f.ownership[model], nil
	})
	f.svc = NewOtohaCatalogServiceWithDeps(
		f.repo,
		fakeOtohaCatalogGroups{otohaCompositeGroupID: f.group},
		otohaResolverPricer{resolver: NewModelPricingResolver(channels, NewBillingService(nil, nil))},
		f.metadata,
		otohaDiagnoserRouting{gateway: f.diagnoser, openai: f.diagnoser, openaiBilling: f.diagnoser, composite: resolver,
			restricted: func(_ context.Context, _ int64, model string) bool { return f.restricted[model] },
			channelMapping: func(ctx context.Context, _ int64, model string) ChannelMappingResult {
				platform, _ := ResolvedTargetPlatformFromContext(ctx)
				if mapping, ok := f.channelMap[platform+":"+model]; ok {
					return mapping
				}
				return ChannelMappingResult{MappedModel: model}
			}},
	)
	return f
}

// otohaRoutesFunc serves the fixture's current composite routes.
type otohaRoutesFunc func() ([]CompositeModelRoute, error)

func (r otohaRoutesFunc) ListByGroup(ctx context.Context, groupID int64, includeDisabled bool) ([]CompositeModelRoute, error) {
	routes, err := r()
	if err != nil {
		return nil, err
	}
	return compositeRouteRepoStub{routes: routes}.ListByGroup(ctx, groupID, includeDisabled)
}
func (r otohaRoutesFunc) Create(context.Context, *CompositeModelRoute) error { return nil }
func (r otohaRoutesFunc) Update(context.Context, *CompositeModelRoute) error { return nil }
func (r otohaRoutesFunc) Delete(context.Context, int64) error                { return nil }
func (r otohaRoutesFunc) DeleteByGroup(context.Context, int64) error         { return nil }

func (f *otohaCompositeFixture) add(t *testing.T, models ...string) {
	t.Helper()
	for _, model := range models {
		_, err := f.svc.CreateEntry(context.Background(), otohaCompositeGroupID, OtohaCatalogEntryInput{ModelID: model, Enabled: true})
		require.NoError(t, err)
	}
}

func (f *otohaCompositeFixture) view(t *testing.T) (*OtohaCatalogAdminView, map[string]OtohaCatalogAdminEntry) {
	t.Helper()
	view, err := f.svc.AdminView(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	byModel := map[string]OtohaCatalogAdminEntry{}
	for _, e := range view.Entries {
		byModel[e.ModelID] = e
	}
	return view, byModel
}

func TestOtohaCompositeCatalogListsEachProvidersModelsAtWhatBillingCharges(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.add(t, "gpt-6-luna", "claude-sonnet-4-5", "claude-opus-4-1", "gemini-2.5-pro", "mystery-model")

	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-luna", "claude-sonnet-4-5"}, otohaModelIDs(catalog),
		"one key gets the openai and the anthropic model; the others cannot be served through /v1/responses")

	luna, sonnet := catalog.Models[0], catalog.Models[1]
	require.Equal(t, 2.0, luna.Price.Input, "the openai price for the GPT model, times the rate 2, not the anthropic one")
	require.Equal(t, 8.0, luna.Price.Output)
	require.Equal(t, 6.0, sonnet.Price.Input)
	require.Equal(t, 30.0, sonnet.Price.Output)
	require.NotNil(t, sonnet.Price.CachedInput)
	require.Equal(t, 0.6, *sonnet.Price.CachedInput)

	view, byModel := f.view(t)
	require.Equal(t, PlatformComposite, view.GroupPlatform)
	require.Equal(t, PlatformOpenAI, byModel["gpt-6-luna"].RoutePlatform)
	require.Equal(t, PlatformAnthropic, byModel["claude-sonnet-4-5"].RoutePlatform)
	require.Empty(t, byModel["gpt-6-luna"].RouteModel, "the model goes upstream under its own name")

	opus := byModel["claude-opus-4-1"]
	require.Equal(t, OtohaCatalogProblemNoAccount, opus.Problem, "no anthropic account serves it")
	require.Equal(t, PlatformAnthropic, opus.RoutePlatform)
	require.NotNil(t, opus.SalePrice, "a hidden model still shows its price, at its provider")
	require.Equal(t, 30.0, opus.SalePrice.Input)

	gemini := byModel["gemini-2.5-pro"]
	require.Equal(t, OtohaCatalogProblemNotViaResponses, gemini.Problem,
		"/v1/responses does not reach Gemini accounts: the Responses handler would send them an Anthropic request")
	require.Equal(t, PlatformGemini, gemini.RoutePlatform)

	mystery := byModel["mystery-model"]
	require.Equal(t, OtohaCatalogProblemNoRoute, mystery.Problem, "the group cannot tell which provider serves it")
	require.Empty(t, mystery.RoutePlatform)

	require.Equal(t, catalog, view.Preview)
	require.Contains(t, f.diagnoser.calls, "openai:gpt-6-luna")
	require.Contains(t, f.diagnoser.calls, "anthropic:claude-sonnet-4-5")
	require.NotContains(t, f.diagnoser.calls, "gemini:gemini-2.5-pro", "a provider the app cannot reach is not asked")
}

func TestOtohaCompositeExplicitRoutePricesAndChecksTheModelItForwards(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.routes = []CompositeModelRoute{{
		ID: 1, GroupID: otohaCompositeGroupID, PublicModel: "otoha-smart", MatchType: CompositeRouteMatchExact,
		TargetPlatform: PlatformAnthropic, UpstreamModel: "claude-sonnet-4-5", Endpoint: CompositeRouteEndpointAny, Enabled: true,
	}}
	f.add(t, "otoha-smart")

	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Equal(t, []string{"otoha-smart"}, otohaModelIDs(catalog))
	require.Equal(t, 6.0, catalog.Models[0].Price.Input, "billed as the anthropic model it is forwarded as")
	require.Equal(t, []string{"anthropic:claude-sonnet-4-5"}, f.diagnoser.calls)

	_, byModel := f.view(t)
	require.Equal(t, PlatformAnthropic, byModel["otoha-smart"].RoutePlatform)
	require.Equal(t, "claude-sonnet-4-5", byModel["otoha-smart"].RouteModel)
}

func TestOtohaCompositeRouteToAProviderWithoutResponsesIsHidden(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.routes = []CompositeModelRoute{{
		ID: 1, GroupID: otohaCompositeGroupID, PublicModel: "otoha-gemini", MatchType: CompositeRouteMatchExact,
		TargetPlatform: PlatformGemini, UpstreamModel: "gemini-2.5-pro", Endpoint: CompositeRouteEndpointAny, Enabled: true,
	}}
	f.diagnoser.models[PlatformGemini] = []string{"gemini-2.5-pro"}
	f.add(t, "otoha-gemini")

	_, byModel := f.view(t)
	require.Equal(t, OtohaCatalogProblemNotViaResponses, byModel["otoha-gemini"].Problem,
		"a Gemini account is not reachable through /v1/responses even when the group has one")
	require.Equal(t, 2.5, byModel["otoha-gemini"].SalePrice.Input)
}

func TestOtohaCompositeAccountOwnershipDecidesTheProvider(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.ownership["house-model"] = CompositeModelOwnership{Matched: true, TargetPlatform: PlatformOpenAI}
	f.diagnoser.models[PlatformOpenAI] = append(f.diagnoser.models[PlatformOpenAI], "house-model")
	f.ownership["shared-model"] = CompositeModelOwnership{Ambiguous: true}
	f.add(t, "house-model", "shared-model")

	_, byModel := f.view(t)
	require.True(t, byModel["house-model"].InCatalog)
	require.Equal(t, PlatformOpenAI, byModel["house-model"].RoutePlatform)
	require.Equal(t, 1.0, byModel["house-model"].SalePrice.Input)
	require.Equal(t, OtohaCatalogProblemNoRoute, byModel["shared-model"].Problem,
		"accounts of two providers claim it; the request would be refused")
}

// When accounts of two providers claim a model, the request is not routed by them; the Anthropic Responses handler
// then goes by the model name, which serves a Claude model and fails a GPT one.
func TestOtohaCompositeContestedModelFollowsTheResponsesHandler(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.ownership["claude-sonnet-4-5"] = CompositeModelOwnership{Ambiguous: true}
	f.ownership["gpt-6-luna"] = CompositeModelOwnership{Ambiguous: true}
	f.add(t, "claude-sonnet-4-5", "gpt-6-luna")

	_, byModel := f.view(t)
	require.True(t, byModel["claude-sonnet-4-5"].InCatalog)
	require.Equal(t, PlatformAnthropic, byModel["claude-sonnet-4-5"].RoutePlatform)
	require.Equal(t, OtohaCatalogProblemNoRoute, byModel["gpt-6-luna"].Problem)
}

// The OpenAI gateway bills what the serving account maps the model to; the catalog prices the same.
func TestOtohaCompositeOpenAIPriceIsTheAccountMappedModels(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.diagnoser.billed["gpt-6-luna"] = []string{"gpt-6-luna-2026"}
	f.add(t, "gpt-6-luna")

	_, byModel := f.view(t)
	luna := byModel["gpt-6-luna"]
	require.Equal(t, 3.0, luna.SalePrice.Input, "gpt-6-luna-2026 at $1.5 times the rate 2")
	require.Equal(t, "gpt-6-luna-2026", luna.BilledModel)
	require.False(t, luna.PriceVaries)

	f.diagnoser.billed["gpt-6-luna"] = []string{"gpt-6-luna-2026", "gpt-6-luna-pro"}
	f.svc.InvalidateGroup(otohaCompositeGroupID)
	_, byModel = f.view(t)
	luna = byModel["gpt-6-luna"]
	require.Equal(t, 10.0, luna.SalePrice.Input, "accounts bill it differently: the catalog shows the highest")
	require.Equal(t, "gpt-6-luna-pro", luna.BilledModel)
	require.True(t, luna.PriceVaries)

	f.diagnoser.billed["gpt-6-luna"] = []string{"gpt-6-luna-unpriced"}
	_, byModel = f.view(t)
	require.Equal(t, 2.0, byModel["gpt-6-luna"].SalePrice.Input, "an unpriced mapped name is billed as the model sent")
	require.Empty(t, byModel["gpt-6-luna"].BilledModel)
}

func TestOtohaCompositeModelTheChannelRestrictsIsHidden(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.restricted["gpt-6-luna"] = true
	f.add(t, "gpt-6-luna")

	_, byModel := f.view(t)
	require.Equal(t, OtohaCatalogProblemChannelRestricted, byModel["gpt-6-luna"].Problem)
}

// A failed route lookup hides the model for this read only; the next read tries again.
func TestOtohaCompositeRouteLookupFailureIsNotCached(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.add(t, "gpt-6-luna")
	f.resolveErr = errors.New("database is down")

	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Empty(t, catalog.Models)

	f.resolveErr = nil
	catalog, err = f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-luna"}, otohaModelIDs(catalog))
}

func TestOtohaCompositePrefillReadsTheRoutedProvider(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.metadata.byModel["claude-sonnet-4-5"] = OtohaModelMetadata{Name: "Claude Sonnet 4.5", Inputs: []string{"text", "image"}, Tools: true}

	draft, err := f.svc.Prefill(context.Background(), otohaCompositeGroupID, "claude-sonnet-4-5")
	require.NoError(t, err)
	require.True(t, draft.MetadataFound)
	require.Equal(t, "Claude Sonnet 4.5", draft.Entry.Name)
	require.Equal(t, PlatformAnthropic, draft.RoutePlatform)
	require.Equal(t, []string{"anthropic:claude-sonnet-4-5"}, f.metadata.seen, "metadata comes from the provider the model is routed to")
	require.NotNil(t, draft.UpstreamPrice)
	require.Equal(t, 3.0, draft.UpstreamPrice.Input)
	require.Equal(t, 6.0, draft.SalePrice.Input)

	draft, err = f.svc.Prefill(context.Background(), otohaCompositeGroupID, "mystery-model")
	require.NoError(t, err, "a model the group cannot route still gets a draft")
	require.Empty(t, draft.RoutePlatform)
	require.Nil(t, draft.UpstreamPrice)
}

// A group of one provider whose accounts /v1/responses does not reach lists nothing the app could not call.
func TestOtohaCatalogOfAGeminiGroupHidesWhatResponsesCannotReach(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.groups[otohaGroupID].Platform = PlatformGemini
	f.pricer["gemini-2.5-pro"] = OtohaModelPrice{Input: 1.25, Output: 10}
	diagnoser := &fakePlatformDiagnoser{models: map[string][]string{PlatformGemini: {"gemini-2.5-pro"}}}
	f.svc = NewOtohaCatalogServiceWithDeps(f.repo, f.groups, f.pricer, f.metadata, otohaDiagnoserRouting{gateway: diagnoser, openai: diagnoser})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gemini-2.5-pro", Enabled: true})

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaCatalogProblemNotViaResponses, view.Entries[0].Problem)
	require.Empty(t, view.Entries[0].RoutePlatform, "a single-provider group needs no provider column")
	require.Empty(t, view.Preview.Models)
}

func TestOtohaSyncedMetadataComesFromTheRoutedProvidersAccounts(t *testing.T) {
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{}},
		{ID: 2, Platform: PlatformAnthropic, Extra: map[string]any{}},
	}
	for i := range accounts {
		accounts[i].Extra[UpstreamModelMetadataExtraKey] = map[string]any{"models": map[string]any{
			"shared-name": map[string]any{"id": "shared-name", "description": strings.ToUpper(accounts[i].Platform)},
		}}
	}
	synced := otohaSyncedAccountMetadata(accounts, PlatformAnthropic, "shared-name")
	require.Len(t, synced, 1)
	require.Equal(t, "ANTHROPIC", synced[0].Description)
	require.Len(t, otohaSyncedAccountMetadata(accounts, "", "shared-name"), 2, "without a provider every account counts")

	mixed := Account{ID: 3, Platform: PlatformAntigravity, Extra: map[string]any{
		"mixed_scheduling": true,
		UpstreamModelMetadataExtraKey: map[string]any{"models": map[string]any{
			"shared-name": map[string]any{"id": "shared-name", "description": "MIXED"},
		}},
	}}
	require.True(t, mixed.IsMixedSchedulingEnabled())
	synced = otohaSyncedAccountMetadata(append(accounts, mixed), PlatformAnthropic, "shared-name")
	require.Len(t, synced, 2, "Antigravity accounts scheduled with Anthropic ones serve the model too")
}

// The group's channel can map a model to another name; the request is forwarded, and by default billed, as that name.
func TestOtohaCompositeChannelMappingIsWhatIsServedAndBilled(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.diagnoser.models[PlatformOpenAI] = append(f.diagnoser.models[PlatformOpenAI], "gpt-6-luna-2026")
	f.channelMap["openai:gpt-6-luna"] = ChannelMappingResult{MappedModel: "gpt-6-luna-2026", Mapped: true, BillingModelSource: BillingModelSourceChannelMapped}
	f.add(t, "gpt-6-luna")

	_, byModel := f.view(t)
	luna := byModel["gpt-6-luna"]
	require.True(t, luna.InCatalog)
	require.Equal(t, 3.0, luna.SalePrice.Input, "billed as the channel's name, gpt-6-luna-2026 at $1.5, times 2")
	require.Equal(t, "gpt-6-luna-2026", luna.BilledModel)
	require.Contains(t, f.diagnoser.calls, "openai:gpt-6-luna-2026", "accounts are asked for the name the channel forwards")

	f.channelMap["openai:gpt-6-luna"] = ChannelMappingResult{MappedModel: "gpt-6-luna-2026", Mapped: true, BillingModelSource: BillingModelSourceRequested}
	_, byModel = f.view(t)
	require.Equal(t, 2.0, byModel["gpt-6-luna"].SalePrice.Input, "a channel that bills the requested name prices the model itself")

	f.channelMap["openai:gpt-6-luna"] = ChannelMappingResult{MappedModel: "gpt-6-luna-2026", Mapped: true, BillingModelSource: BillingModelSourceUpstream}
	f.diagnoser.billed["gpt-6-luna-2026"] = []string{"gpt-6-luna-pro"}
	_, byModel = f.view(t)
	require.Equal(t, 10.0, byModel["gpt-6-luna"].SalePrice.Input, "billed as the account maps the channel's name")
}

// A model routed because an account claims it is served only by the accounts that claim it.
func TestOtohaCompositeOwnedModelIsBilledByTheClaimingAccounts(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.ownership["house-model"] = CompositeModelOwnership{Matched: true, TargetPlatform: PlatformOpenAI}
	f.diagnoser.models[PlatformOpenAI] = append(f.diagnoser.models[PlatformOpenAI], "house-model")
	f.add(t, "house-model", "gpt-6-luna")

	f.view(t)
	require.ElementsMatch(t, []string{"house-model", ""}, f.diagnoser.claimedBy,
		"the owned model asks for its claiming accounts only; the model routed by name asks for every account")
}

func TestOtohaCompositeBilledNameLookupFailureIsNotCached(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.add(t, "gpt-6-luna")
	f.diagnoser.billedErr = errors.New("database is down")
	_, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)

	f.diagnoser.billedErr = nil
	f.diagnoser.billed["gpt-6-luna"] = []string{"gpt-6-luna-pro"}
	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Equal(t, 10.0, catalog.Models[0].Price.Input, "the read after the failure prices it afresh")
}
