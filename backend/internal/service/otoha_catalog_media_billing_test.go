//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TASK-81: the price the account page shows for a second of video, a voice unit or a million Jev tokens is the price
// the request is charged. For each, the catalog's price equals the ActualCost RecordUsage writes.

type otohaMediaBillingFixture struct {
	usageRepo *openAIRecordUsageLogRepoStub
	openai    *OpenAIGatewayService
	group     *Group
	catalog   *OtohaCatalogService
}

func newOtohaMediaBillingFixture(t *testing.T, mutate func(*Group)) *otohaMediaBillingFixture {
	t.Helper()
	f := &otohaMediaBillingFixture{usageRepo: &openAIRecordUsageLogRepoStub{inserted: true}}
	f.openai = newOpenAIRecordUsageServiceForTest(f.usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	f.openai.resolver = NewModelPricingResolver(nil, f.openai.billingService)
	f.group = &Group{
		ID: otohaGroupID, Name: "Otoha", Platform: PlatformGrok, Status: StatusActive, Hydrated: true,
		RateMultiplier: 1.5, ImageRateMultiplier: 1, VideoRateMultiplier: 1,
	}
	if mutate != nil {
		mutate(f.group)
	}
	f.catalog = f.catalogWith(t, f.openai.resolver)
	return f
}

func (f *otohaMediaBillingFixture) catalogWith(t *testing.T, resolver *ModelPricingResolver) *OtohaCatalogService {
	t.Helper()
	store := newFakeOtohaMediaStore()
	require.NoError(t, store.SaveMediaEntries(context.Background(), f.group.ID, []OtohaMediaEntry{
		{Kind: OtohaMediaKindVideo, ModelID: "grok-imagine-video", Name: "v1", Enabled: true, SortOrder: 10},
		{Kind: OtohaMediaKindVideo, ModelID: "grok-imagine-video-1.5", Name: "v15", Enabled: true, SortOrder: 20},
		{Kind: OtohaMediaKindVoice, ModelID: "tts", Name: "tts", Enabled: true, SortOrder: 30},
		{Kind: OtohaMediaKindVoice, ModelID: "stt", Name: "stt", Enabled: true, SortOrder: 40},
		{Kind: OtohaMediaKindQuick, ModelID: "jev-latest", Name: "Jev", Enabled: true, SortOrder: 50},
	}))
	return NewOtohaCatalogServiceWithDeps(
		newFakeOtohaCatalogRepo(), fakeOtohaCatalogGroups{f.group.ID: f.group}, otohaResolverPricer{resolver: resolver}, fakeOtohaMetadata{}, fakeOtohaRouting{},
	).WithMedia(store, otohaOpenAIMediaPricer{openai: f.openai}, fakeOtohaMediaRouting{})
}

func (f *otohaMediaBillingFixture) shown(t *testing.T) *OtohaCatalog {
	t.Helper()
	catalog, err := f.catalog.CatalogForGroup(context.Background(), f.group.ID)
	require.NoError(t, err)
	require.NotNil(t, catalog)
	return catalog
}

func (f *otohaMediaBillingFixture) record(t *testing.T, result *OpenAIForwardResult) float64 {
	t.Helper()
	groupID := f.group.ID
	result.RequestID = "media-" + result.Model + "-" + result.VideoResolution
	require.NoError(t, f.openai.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result:  result,
		APIKey:  &APIKey{ID: 10, GroupID: &groupID, Group: f.group},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformGrok, Type: AccountTypeAPIKey},
	}))
	require.NotNil(t, f.usageRepo.lastLog)
	return f.usageRepo.lastLog.ActualCost
}

func (f *otohaMediaBillingFixture) requireVideoEqualsCharge(t *testing.T) {
	t.Helper()
	catalog := f.shown(t)
	require.Len(t, catalog.VideoModels, 2)
	for _, model := range catalog.VideoModels {
		require.NotEmpty(t, model.Resolutions)
		for _, resolution := range model.Resolutions {
			charged := f.record(t, &OpenAIForwardResult{Model: model.ID, VideoCount: 1, VideoResolution: resolution.Resolution, VideoDurationSeconds: 10})
			require.InDelta(t, resolution.Price*10, charged, 1e-5, "%s %s: the page shows %v a second, ten seconds are charged %v", model.ID, resolution.Resolution, resolution.Price, charged)
			require.Greater(t, resolution.Price, 0.0)
		}
	}
}

func TestOtohaMediaVideoPriceEqualsTheChargeWithBillingsDefaults(t *testing.T) {
	f := newOtohaMediaBillingFixture(t, nil)
	f.requireVideoEqualsCharge(t)

	catalog := f.shown(t)
	require.Equal(t, []OtohaVideoResolution{{"480p", 0.075}, {"720p", 0.105}}, catalog.VideoModels[0].Resolutions, "billing's 0.05 and 0.07 a second, times the rate 1.5")
	require.Equal(t, []OtohaVideoResolution{{"480p", 0.12}, {"720p", 0.21}, {"1080p", 0.375}}, catalog.VideoModels[1].Resolutions)
}

func TestOtohaMediaVideoPriceEqualsTheChargeWithTheGroupsVideoPrices(t *testing.T) {
	f := newOtohaMediaBillingFixture(t, func(g *Group) {
		g.VideoPrice480P, g.VideoPrice720P, g.VideoPrice1080P = otohaFloat(0.04), otohaFloat(0.06), otohaFloat(0.2)
		g.VideoModelPrices = map[string]map[string]float64{"grok-imagine-video-1.5": {"720p": 0.1}}
	})
	f.requireVideoEqualsCharge(t)

	catalog := f.shown(t)
	require.Equal(t, 0.06, catalog.VideoModels[0].Resolutions[0].Price, "the group's 480p price 0.04 times 1.5")
	require.Equal(t, 0.15, catalog.VideoModels[1].Resolutions[1].Price, "the model's own 720p price 0.1 times 1.5 beats the group's")
}

func TestOtohaMediaVideoPriceEqualsTheChargeAtTheGroupsOwnVideoRate(t *testing.T) {
	f := newOtohaMediaBillingFixture(t, func(g *Group) {
		g.VideoRateIndependent, g.VideoRateMultiplier = true, 0.4
	})
	f.requireVideoEqualsCharge(t)
	require.Equal(t, 0.02, f.shown(t).VideoModels[0].Resolutions[0].Price)
}

func TestOtohaMediaVideoHasNoPricePerSecondWhenTheChannelChargesPerRequestOrByToken(t *testing.T) {
	for name, mode := range map[string]BillingMode{"per request": BillingModePerRequest, "by token": BillingModeToken} {
		t.Run(name, func(t *testing.T) {
			groupID := int64(77)
			price := 0.5
			cache := newEmptyChannelCache()
			cache.pricingByGroupModel[channelModelKey{groupID: groupID, model: "grok-imagine-video"}] = &ChannelModelPricing{
				BillingMode: mode, PerRequestPrice: &price, InputPrice: &price, OutputPrice: &price,
			}
			cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
			cache.groupPlatform[groupID] = ""
			cache.loadedAt = time.Now()
			cs := &ChannelService{}
			cs.cache.Store(cache)
			resolver := NewModelPricingResolver(cs, NewBillingService(&config.Config{}, nil))
			openai := &OpenAIGatewayService{billingService: resolver.billingService, resolver: resolver}
			group := &Group{ID: groupID, Platform: PlatformGrok, Hydrated: true, RateMultiplier: 1}

			_, ok := otohaOpenAIMediaPricer{openai: openai}.VideoSecondPrice(context.Background(), group, "grok-imagine-video", "720p")
			require.False(t, ok, "billing does not charge it by the second, so there is no price per second to show")

			_, ok = otohaOpenAIMediaPricer{openai: openai}.VideoSecondPrice(context.Background(), group, "grok-imagine-video-1.5", "720p")
			require.True(t, ok, "another model keeps billing's price")
		})
	}
}

func TestOtohaMediaVoicePriceEqualsTheCharge(t *testing.T) {
	for name, mutate := range map[string]func(*Group){
		"billing defaults": nil,
		"the group's own prices": func(g *Group) {
			g.AudioTTSPricePerMillionChars, g.AudioSTTPricePerHour = otohaFloat(20), otohaFloat(0.3)
		},
		"a free function": func(g *Group) { g.AudioSTTPricePerHour = otohaFloat(0) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newOtohaMediaBillingFixture(t, mutate)
			catalog := f.shown(t)
			require.Len(t, catalog.VoiceModels, 2)
			for _, model := range catalog.VoiceModels {
				// 250 000 characters of text, or two and a half hours of audio.
				units := 0.25
				if model.Mode == OtohaVoiceModeSTT {
					units = 2.5
				}
				charged := f.record(t, &OpenAIForwardResult{Model: model.Mode, AudioUsage: &AudioUsage{Mode: model.Mode, DurationOrUnits: units}})
				require.InDelta(t, model.Price*units, charged, 1e-6, "%s: the page shows %v per %s, %v units are charged %v", model.ID, model.Price, model.Per, units, charged)
			}
		})
	}
	f := newOtohaMediaBillingFixture(t, nil)
	voice := f.shown(t).VoiceModels
	require.Equal(t, 22.5, voice[0].Price, "15 for a million characters, times 1.5")
	require.Equal(t, 0.15, voice[1].Price, "0.10 for an hour, times 1.5")
}

func TestOtohaMediaVoiceFollowsTheGroupRateNotTheVideoRate(t *testing.T) {
	f := newOtohaMediaBillingFixture(t, func(g *Group) { g.VideoRateIndependent, g.VideoRateMultiplier = true, 0.1 })
	require.Equal(t, 22.5, f.shown(t).VoiceModels[0].Price)
	charged := f.record(t, &OpenAIForwardResult{Model: "tts", AudioUsage: &AudioUsage{Mode: "tts", DurationOrUnits: 1}})
	require.InDelta(t, 22.5, charged, 1e-9)
}

func TestOtohaMediaQuickPriceEqualsTheCharge(t *testing.T) {
	f := newOtohaMediaBillingFixture(t, nil)
	quick := f.shown(t).QuickModels
	require.Len(t, quick, 1)

	// A million input tokens and nothing else: Jev bills input tokens only.
	cost, err := f.openai.billingService.CalculateCost("jev-latest", UsageTokens{InputTokens: 1_000_000, OutputTokens: 500_000}, 1.5)
	require.NoError(t, err)
	require.InDelta(t, quick[0].Price, cost.ActualCost, 1e-6, "a million input tokens are charged what the page shows; output tokens add nothing")
	require.Equal(t, 0.063, quick[0].Price)
	require.Zero(t, quick[0].OutputPrice)
	require.Equal(t, "input", quick[0].Billed)
}

// ---- where the group sends them ----

type fakeOtohaCompositeResolver struct {
	decisions map[string]CompositeRouteDecision
	err       error
}

func (r fakeOtohaCompositeResolver) Resolve(_ context.Context, _ int64, model, _ string) (CompositeRouteDecision, error) {
	if r.err != nil {
		return CompositeRouteDecision{}, r.err
	}
	if decision, ok := r.decisions[model]; ok {
		return decision, nil
	}
	return CompositeRouteDecision{}, nil
}

func mediaRouting(composite otohaCompositeResolver, models map[string][]string) (otohaDiagnoserRouting, *fakePlatformDiagnoser) {
	diagnoser := &fakePlatformDiagnoser{billed: map[string][]string{}, models: models}
	return otohaDiagnoserRouting{gateway: diagnoser, openai: diagnoser, composite: composite}, diagnoser
}

func TestOtohaMediaRouteVoiceNeedsAGrokGroup(t *testing.T) {
	routing, _ := mediaRouting(nil, nil)

	require.Equal(t, OtohaModelRoute{}, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformGrok}, OtohaMediaKindVoice, "tts"))
	composite := routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformComposite}, OtohaMediaKindVoice, "tts")
	require.Equal(t, OtohaCatalogProblemNoRoute, composite.Problem, "the voice endpoints answer a Grok group only; a composite group gets a 404")
	require.Equal(t, OtohaCatalogProblemNoRoute, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformOpenAI}, OtohaMediaKindVoice, "stt").Problem)
}

func TestOtohaMediaRouteVideoInACompositeGroupFollowsTheRouteToGrok(t *testing.T) {
	routing, diagnoser := mediaRouting(fakeOtohaCompositeResolver{decisions: map[string]CompositeRouteDecision{
		"grok-imagine-video":     {Matched: true, TargetPlatform: PlatformGrok, UpstreamModel: "grok-imagine-video"},
		"grok-imagine-video-1.5": {Matched: true, TargetPlatform: PlatformOpenAI},
	}}, map[string][]string{PlatformGrok: {"grok-imagine-video"}})
	group := &Group{ID: 1, Platform: PlatformComposite}

	ok := routing.MediaRoute(context.Background(), group, OtohaMediaKindVideo, "grok-imagine-video")
	require.Empty(t, ok.Problem)
	require.Equal(t, PlatformGrok, ok.Platform)
	require.Contains(t, diagnoser.calls, PlatformGrok+":grok-imagine-video")

	wrong := routing.MediaRoute(context.Background(), group, OtohaMediaKindVideo, "grok-imagine-video-1.5")
	require.Equal(t, OtohaCatalogProblemNoRoute, wrong.Problem, "an explicit route sends it to another provider")

	unrouted := routing.MediaRoute(context.Background(), group, OtohaMediaKindVideo, "grok-imagine-video-2")
	require.Equal(t, OtohaCatalogProblemNoAccount, unrouted.Problem, "the model's name says Grok, but no Grok account serves it")
}

func TestOtohaMediaRouteQuickInACompositeGroupGoesToTypeSafeByTheModelName(t *testing.T) {
	routing, diagnoser := mediaRouting(fakeOtohaCompositeResolver{}, map[string][]string{PlatformTypeSafe: {"jev-latest"}})
	group := &Group{ID: 1, Platform: PlatformComposite}

	got := routing.MediaRoute(context.Background(), group, OtohaMediaKindQuick, "jev-latest")

	require.Empty(t, got.Problem)
	require.Equal(t, PlatformTypeSafe, got.Platform)
	require.Contains(t, diagnoser.calls, PlatformTypeSafe+":jev-latest")

	none, _ := mediaRouting(fakeOtohaCompositeResolver{}, map[string][]string{PlatformTypeSafe: {}})
	require.Equal(t, OtohaCatalogProblemNoAccount, none.MediaRoute(context.Background(), group, OtohaMediaKindQuick, "jev-latest").Problem)
}

func TestOtohaMediaRouteOtherGroupsAndChannelLimits(t *testing.T) {
	routing, _ := mediaRouting(nil, map[string][]string{PlatformGrok: {"grok-imagine-video"}, PlatformTypeSafe: {"jev-latest"}})

	require.Empty(t, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformGrok}, OtohaMediaKindVideo, "grok-imagine-video").Problem)
	require.Empty(t, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformTypeSafe}, OtohaMediaKindQuick, "jev-latest").Problem)
	require.Equal(t, OtohaCatalogProblemNoRoute, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformOpenAI}, OtohaMediaKindVideo, "grok-imagine-video").Problem)
	require.Equal(t, OtohaCatalogProblemNoRoute, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformGrok}, OtohaMediaKindQuick, "jev-latest").Problem)

	routing.restricted = func(context.Context, int64, string) bool { return true }
	require.Equal(t, OtohaCatalogProblemChannelRestricted, routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformGrok}, OtohaMediaKindVideo, "grok-imagine-video").Problem)
}

func TestOtohaMediaRouteALookupFailureIsMarkedSoItIsNotKept(t *testing.T) {
	routing, _ := mediaRouting(fakeOtohaCompositeResolver{err: context.DeadlineExceeded}, nil)

	got := routing.MediaRoute(context.Background(), &Group{ID: 1, Platform: PlatformComposite}, OtohaMediaKindVideo, "grok-imagine-video")

	require.True(t, got.Failed)
	require.NotEmpty(t, got.Problem)
}

// ---- reading the group's accounts ----

type fakeOtohaMediaGroupAccounts struct{ AccountRepository }

func (fakeOtohaMediaGroupAccounts) ListByGroup(context.Context, int64) ([]Account, error) {
	return []Account{
		*openAIKeyAccount(1, "gpt", nil),
		*grokAccount(2, "grok", AccountTypeOAuth),
		*typeSafeAccount(3, "jev"),
		{ID: 4, Name: "claude", Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
	}, nil
}

func TestOtohaMediaAccountSourceAsksGrokAccountsAndTakesTypeSafeAsTheyAre(t *testing.T) {
	source := NewOtohaMediaAccountSource(fakeOtohaMediaGroupAccounts{}, fakeOtohaModelLister{
		2: {Models: listed("grok-imagine-video")},
	})

	got, err := source.ListGroupMediaAccounts(context.Background(), 5)

	require.NoError(t, err)
	require.Len(t, got, 2, "OpenAI and Anthropic accounts offer none of these")
	require.Equal(t, "grok", got[0].Account.Name)
	require.Equal(t, "grok-imagine-video", got[0].Models[0].ID)
	require.Equal(t, "jev", got[1].Account.Name)
	require.Empty(t, got[1].Err, "a TypeSafe account has no list to read, which is not a failure")
}

func TestOtohaMediaAccountSourceWithoutAListerUsesTheGatewaysTable(t *testing.T) {
	source := NewOtohaMediaAccountSource(fakeOtohaMediaGroupAccounts{}, nil)

	got, err := source.ListGroupMediaAccounts(context.Background(), 5)

	require.NoError(t, err)
	require.Equal(t, OtohaImageListUnsupported, got[0].Err)
	require.Contains(t, mediaCandidates(got...), "video/grok-imagine-video")
}
