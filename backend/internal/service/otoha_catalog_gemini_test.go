//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TASK-85: Gemini picture and video models from the group's Gemini accounts. What the gateway really serves decides
// the format named for each model: a Gemini picture model is reached on the native generateContent endpoint, not on
// the OpenAI images endpoints, so the app cannot use it (app is false); Veo has no endpoint at all.

func geminiKeyAccount(id int64, name string, mapping map[string]any) *Account {
	creds := map[string]any{"api_key": "AIza-test"}
	if mapping != nil {
		creds["model_mapping"] = mapping
	}
	return &Account{ID: id, Name: name, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: creds}
}

// geminiModelListBody is a GET /v1beta/models answer of the Gemini API: ids are "models/<id>", the display name is
// displayName, and the only thing said about what a model does is supportedGenerationMethods.
const geminiModelListBody = `{"models":[
	{"name":"models/gemini-3.5-flash","displayName":"Gemini 3.5 Flash","supportedGenerationMethods":["generateContent","countTokens"]},
	{"name":"models/gemini-3.1-flash-image","displayName":"Nano Banana 2","supportedGenerationMethods":["generateContent","countTokens","batchGenerateContent"]},
	{"name":"models/nano-banana-pro-preview","displayName":"Nano Banana Pro","supportedGenerationMethods":["generateContent","countTokens"]},
	{"name":"models/veo-3.1-generate-preview","displayName":"Veo 3.1","supportedGenerationMethods":["predictLongRunning"]},
	{"name":"models/imagen-4.0-generate-001","displayName":"Imagen 4","supportedGenerationMethods":["predict"]},
	{"name":"models/text-embedding-004","displayName":"Embedding","supportedGenerationMethods":["embedContent"]}]}`

// ---- reading a Gemini list ----

func TestOtohaListedModelsReadAGeminiList(t *testing.T) {
	got := byListedID(parseOtohaListedModels([]byte(geminiModelListBody), false))

	require.Len(t, got, 6, "ids come from the name field, without the models/ prefix")
	require.Equal(t, "Nano Banana 2", got["gemini-3.1-flash-image"].Name, "the display name is displayName")
	require.Empty(t, got["gemini-3.1-flash-image"].Kind, "a generateContent model says nothing about drawing")
	require.Empty(t, got["gemini-3.5-flash"].Kind)

	veo := got["veo-3.1-generate-preview"]
	require.True(t, veo.Video, "predictLongRunning is the call that makes a video")
	require.Equal(t, "supported_generation_methods", veo.VideoDeclaredBy)
	require.False(t, got["gemini-3.1-flash-image"].Video)

	imagen := got["imagen-4.0-generate-001"]
	require.Equal(t, OtohaListedKindImage, imagen.Kind, "predict without generateContent is Imagen, which only draws")
	require.Equal(t, "supported_generation_methods", imagen.DeclaredBy)
}

// ---- the known Gemini picture families ----

func TestOtohaKnownImageFamilyOfGemini(t *testing.T) {
	for _, id := range []string{"gemini-3.1-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image-preview", "gemini-2.5-flash-image", "nano-banana-pro-preview", "nano-banana-2"} {
		require.True(t, otohaKnownImageFamily(PlatformGemini, id), id)
	}
	for _, id := range []string{"gemini-3.5-flash", "gemini-3.1-pro-preview", "gemini-2.5-flash-preview-tts", "veo-3.1-generate-preview", "imagen-4.0-generate-001", "gpt-image-2"} {
		require.False(t, otohaKnownImageFamily(PlatformGemini, id), id)
	}
	require.False(t, otohaKnownImageFamily(PlatformOpenAI, "gemini-3.1-flash-image"), "a family belongs to its platform")
	require.True(t, geminiGenerateContentImageModel("nano-banana-pro-preview"))
	require.False(t, geminiGenerateContentImageModel("imagen-4.0-generate-001"), "the gateway forwards generateContent only")
}

func TestOtohaImageCandidatesFromAGeminiKey(t *testing.T) {
	listing := OtohaAccountListing{
		Account: geminiKeyAccount(12, "Gemini AI Studio", nil),
		Models:  parseOtohaListedModels([]byte(geminiModelListBody), false),
	}

	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{listing}))

	require.Len(t, got, 3)
	flashImage := got["gemini-3.1-flash-image"]
	require.Equal(t, OtohaImageEvidenceKnownFamily, flashImage.Evidence)
	require.Equal(t, PlatformGemini, flashImage.Platform)
	require.Equal(t, "Nano Banana 2", flashImage.Name)
	require.Equal(t, 1, flashImage.Served)
	require.Equal(t, OtohaImageEvidenceKnownFamily, got["nano-banana-pro-preview"].Evidence)
	require.Equal(t, OtohaImageEvidenceDeclared, got["imagen-4.0-generate-001"].Evidence)
	require.NotContains(t, got, "gemini-3.5-flash")
	require.NotContains(t, got, "veo-3.1-generate-preview", "a video model is not a picture model")
}

func TestOtohaImageCandidatesFromAGeminiKeySkipModelsItsMappingDoesNotServe(t *testing.T) {
	account := geminiKeyAccount(12, "mapped", map[string]any{"gemini-3.5-flash": "gemini-3.5-flash"})
	listing := OtohaAccountListing{Account: account, Models: listed("gemini-3.1-flash-image")}

	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{listing}))

	require.Equal(t, 0, got["gemini-3.1-flash-image"].Served, "the key's model mapping lists only Flash")
}

func TestOtohaListingSourceAlsoAsksGeminiAccounts(t *testing.T) {
	source := &otohaAccountListingSource{accounts: fakeOtohaGroupAccountsWithGemini{}, lister: fakeOtohaModelLister{
		1: {Models: listed("gpt-image-2")},
		4: {Models: listed("gemini-3.1-flash-image")},
	}}

	got, err := source.ListGroupAccountModels(context.Background(), 5)

	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "gemini", got[1].Account.Name)
	require.Equal(t, "gemini-3.1-flash-image", got[1].Models[0].ID)
}

type fakeOtohaGroupAccountsWithGemini struct{ AccountRepository }

func (fakeOtohaGroupAccountsWithGemini) ListByGroup(context.Context, int64) ([]Account, error) {
	return []Account{
		*openAIKeyAccount(1, "gpt", nil),
		{ID: 3, Name: "claude", Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
		*geminiKeyAccount(4, "gemini", nil),
	}, nil
}

// ---- the entry made from a candidate ----

func TestOtohaImagePlanGivesAGeminiPictureModelItsDocumentedSmallestDefaults(t *testing.T) {
	candidate := otohaImageCandidate{ModelID: "gemini-3.1-flash-image", Name: "Nano Banana 2", Platform: PlatformGemini, Accounts: []string{"g"}, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}

	got := plan([]otohaImageCandidate{candidate}, nil, true, noReason)

	require.Len(t, got.Create, 1)
	entry := got.Create[0]
	require.Equal(t, []string{"1024x1024"}, entry.Sizes)
	require.Empty(t, entry.Qualities)
	require.True(t, entry.Edits, "reference pictures go in the same call")
	require.Equal(t, "Nano Banana 2", entry.Name)
}

// ---- the route of a Gemini picture model ----

func TestOtohaImageRouteNamesTheFormatTheGatewayServes(t *testing.T) {
	gateway := &fakeOtohaDiagnoser{supported: true}
	openai := &fakeOtohaDiagnoser{supported: true}
	routing := otohaDiagnoserRouting{gateway: gateway, openai: openai}
	ctx := context.Background()
	composite := &Group{ID: 1, Platform: PlatformComposite}

	gemini := routing.ImageRoute(ctx, composite, "gemini-3.1-flash-image")
	require.Empty(t, gemini.Problem)
	require.Equal(t, PlatformGemini, gemini.Platform)
	require.Equal(t, OtohaImageAPIGemini, gemini.API, "the images endpoints answer 404 for a Gemini model; generateContent serves it")
	require.Equal(t, []string{"gemini:gemini-3.1-flash-image"}, gateway.calls, "the Gemini accounts are asked, through the Anthropic gateway")

	gpt := routing.ImageRoute(ctx, composite, "gpt-image-2")
	require.Empty(t, gpt.Problem)
	require.Equal(t, PlatformOpenAI, gpt.Platform)
	require.Equal(t, OtohaImageAPI, gpt.API)

	own := routing.ImageRoute(ctx, &Group{ID: 2, Platform: PlatformGemini}, "gemini-3.1-flash-image")
	require.Empty(t, own.Problem)
	require.Equal(t, OtohaImageAPIGemini, own.API)

	missing := otohaDiagnoserRouting{gateway: &fakeOtohaDiagnoser{}, openai: openai}.ImageRoute(ctx, composite, "gemini-3.1-flash-image")
	require.Equal(t, OtohaCatalogProblemNoAccount, missing.Problem)
	require.Equal(t, OtohaImageAPIGemini, missing.API)
}

func TestOtohaImageRouteOfANameOnlyTheAccountOwnsGoesToGemini(t *testing.T) {
	gateway := &fakeOtohaDiagnoser{supported: true}
	resolver := NewCompositeRouteResolver(nil)
	resolver.SetModelOwnershipResolver(func(_ context.Context, _ int64, model string) (CompositeModelOwnership, error) {
		if model == "nano-banana-pro-preview" {
			return CompositeModelOwnership{Matched: true, TargetPlatform: PlatformGemini}, nil
		}
		return CompositeModelOwnership{}, nil
	})
	routing := otohaDiagnoserRouting{gateway: gateway, composite: resolver}

	route := routing.ImageRoute(context.Background(), &Group{ID: 1, Platform: PlatformComposite}, "nano-banana-pro-preview")

	require.Empty(t, route.Problem)
	require.Equal(t, PlatformGemini, route.Platform)
	require.Equal(t, OtohaImageAPIGemini, route.API)
}

// ---- the catalog the app reads ----

func TestOtohaImageModelJSONNamesTheFormatAndWhetherTheAppCanUseIt(t *testing.T) {
	f := newOtohaImageFixture()
	f.groups[otohaGroupID].RateMultiplier = 1.5
	f.pricer["gemini-3.1-flash-image"] = map[string]float64{"1K": 0.0672, "2K": 0.1008, "4K": 0.1344}
	f.addImage(t, gptImageInput())
	f.addImage(t, OtohaImageEntryInput{ModelID: "gemini-3.1-flash-image", Name: "Nano Banana 2", Enabled: true, Sizes: []string{"1024x1024"}, Edits: true})
	f.svc.WithImages(f.images, f.pricer, fakeGeminiImageRouting{fakeOtohaImageRouting: f.routing})

	models := f.catalog(t, otohaGroupID).ImageModels
	require.Len(t, models, 2)
	gpt, err := json.Marshal(models[0])
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "gpt-image-2", "name": "GPT Image 2", "api": "openai-images", "endpoint": "POST /v1/images/generations", "app": true,
		"edits": true, "qualities": ["low", "medium", "high"], "currency": "USD", "per": "image",
		"sizes": [
			{"size": "1024x1024", "tier": "1K", "price": 0.06},
			{"size": "1536x1024", "tier": "2K", "price": 0.09},
			{"size": "3840x2160", "tier": "4K", "price": 0.18}
		]
	}`, string(gpt))

	gemini, err := json.Marshal(models[1])
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "gemini-3.1-flash-image", "name": "Nano Banana 2", "api": "gemini-generate-content",
		"endpoint": "POST /v1beta/models/gemini-3.1-flash-image:generateContent", "app": false,
		"edits": true, "qualities": [], "currency": "USD", "per": "image",
		"sizes": [{"size": "1024x1024", "tier": "1K", "price": 0.1008}]
	}`, string(gemini), "$0.0672 before the group rate 1.5")
}

func TestOtohaAdminViewNamesTheFormatOfEachPictureModel(t *testing.T) {
	f := newOtohaImageFixture()
	f.pricer["gemini-3.1-flash-image"] = map[string]float64{"1K": 0.0672, "2K": 0.1008, "4K": 0.1344}
	f.addImage(t, gptImageInput())
	f.addImage(t, OtohaImageEntryInput{ModelID: "gemini-3.1-flash-image", Enabled: true, Sizes: []string{"1024x1024"}})
	f.svc.WithImages(f.images, f.pricer, fakeGeminiImageRouting{fakeOtohaImageRouting: f.routing})

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)

	apis := map[string]string{}
	for _, e := range view.ImageEntries {
		apis[e.ModelID] = e.RouteAPI
	}
	require.Equal(t, map[string]string{"gpt-image-2": "", "gemini-3.1-flash-image": OtohaImageAPIGemini}, apis,
		"a route that names no format is the OpenAI images one, which needs no mark")
	body, err := json.Marshal(view.ImageEntries[1])
	require.NoError(t, err)
	require.Contains(t, string(body), `"route_api":"gemini-generate-content"`)
}

// fakeGeminiImageRouting sends a Gemini model to the native format, the others as the wrapped routing does.
type fakeGeminiImageRouting struct{ fakeOtohaImageRouting }

func (r fakeGeminiImageRouting) ImageRoute(ctx context.Context, group *Group, modelID string) OtohaModelRoute {
	route := r.fakeOtohaImageRouting.ImageRoute(ctx, group, modelID)
	if len(modelID) >= 6 && modelID[:6] == "gemini" {
		route.Platform, route.API = PlatformGemini, OtohaImageAPIGemini
	}
	return route
}

// ---- discovery over Gemini accounts ----

func TestOtohaDiscoverImagesAddsGeminiPictureModelsAndReportsWhatTheGatewayCannotServe(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{
		Account: geminiKeyAccount(12, "Gemini AI Studio", nil),
		Models:  parseOtohaListedModels([]byte(geminiModelListBody), false),
	})
	f.groups[otohaGroupID].Platform = PlatformGemini
	f.pricer["gemini-3.1-flash-image"] = map[string]float64{"1K": 0.0672, "2K": 0.1008, "4K": 0.1344}

	report := f.discover(t)

	results := map[string]OtohaImageDiscoveryCandidate{}
	for _, c := range report.Candidates {
		results[c.ModelID] = c
	}
	require.Equal(t, OtohaImageResultAdded, results["gemini-3.1-flash-image"].Result)
	require.Equal(t, OtohaImageResultNotAdded, results["nano-banana-pro-preview"].Result)
	require.Equal(t, OtohaCatalogProblemNoPrice, results["nano-banana-pro-preview"].Reason,
		"billing has no price of its own for the name; it would charge the built-in default")
	require.Equal(t, OtohaImageResultNotAdded, results["imagen-4.0-generate-001"].Result)
	require.Equal(t, OtohaImageReasonGatewayNotSupported, results["imagen-4.0-generate-001"].Reason, "only generateContent is forwarded")
	require.Contains(t, f.entries(t), "gemini-3.1-flash-image")
	require.Len(t, f.entries(t), 1)
	require.Equal(t, 3, report.Accounts[0].ImageModels)
}

// ---- billing: the price shown is the price charged ----

type geminiImageBillingFixture struct {
	usageRepo *openAIRecordUsageLogRepoStub
	gateway   *GatewayService
	group     *Group
	catalog   *OtohaCatalogService
}

func newGeminiImageBillingFixture(t *testing.T, mutate func(*Group)) *geminiImageBillingFixture {
	t.Helper()
	f := &geminiImageBillingFixture{usageRepo: &openAIRecordUsageLogRepoStub{inserted: true}}
	f.gateway = newGatewayRecordUsageServiceForTest(f.usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-3.1-flash-image": {OutputCostPerImage: 0.0672},
		"gemini-3-pro-image":     {OutputCostPerImage: 0.134},
	}}
	f.gateway.billingService = NewBillingService(&config.Config{}, pricing)
	f.gateway.resolver = NewModelPricingResolver(nil, f.gateway.billingService)
	f.group = &Group{
		ID: otohaGroupID, Name: "Otoha", Platform: PlatformGemini, Status: StatusActive, Hydrated: true,
		RateMultiplier: 1.5, ImageRateMultiplier: 1, AllowImageGeneration: true,
	}
	if mutate != nil {
		mutate(f.group)
	}
	f.catalog = NewOtohaCatalogServiceWithDeps(
		newFakeOtohaCatalogRepo(), fakeOtohaCatalogGroups{otohaGroupID: f.group}, fakeOtohaPricer{}, fakeOtohaMetadata{}, fakeOtohaRouting{},
	).WithImages(newFakeOtohaImageRepo(), otohaImagePricerByPlatform{openai: otohaOpenAIImagePricer{}, gemini: otohaGeminiImagePricer{gateway: f.gateway}}, nil)
	f.gateway.SetOtohaImagePriceOverrides(f.catalog)
	return f
}

// charge records one Gemini picture of a size tier and returns what was charged.
func (f *geminiImageBillingFixture) charge(t *testing.T, model, tier string) float64 {
	t.Helper()
	groupID := f.group.ID
	err := f.gateway.RecordUsage(context.Background(), &RecordUsageInput{
		Result:  &ForwardResult{RequestID: "gemini-img-" + model + tier, Model: model, Duration: time.Second, ImageCount: 1, ImageSize: tier},
		APIKey:  &APIKey{ID: 10, GroupID: &groupID, Group: f.group},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformGemini, Type: AccountTypeAPIKey},
	})
	require.NoError(t, err)
	require.NotNil(t, f.usageRepo.lastLog)
	require.Equal(t, 1, f.usageRepo.lastLog.ImageCount)
	return f.usageRepo.lastLog.ActualCost
}

func (f *geminiImageBillingFixture) requireCatalogEqualsCharge(t *testing.T, model string) {
	t.Helper()
	catalog, err := f.catalog.CatalogForGroup(context.Background(), f.group.ID)
	require.NoError(t, err)
	require.NotNil(t, catalog)
	var shown *OtohaImageModel
	for i := range catalog.ImageModels {
		if catalog.ImageModels[i].ID == model {
			shown = &catalog.ImageModels[i]
		}
	}
	require.NotNil(t, shown, "%s is in the catalog", model)
	require.Len(t, shown.Sizes, 3)
	for _, size := range shown.Sizes {
		charged := f.charge(t, model, size.Tier)
		require.InDelta(t, size.Price, charged, 1e-6, "%s %s: the catalog shows %v, the charge is %v", model, size.Tier, size.Price, charged)
		require.Greater(t, size.Price, 0.0)
	}
}

var geminiImageSizes = []string{"1024x1024", "2048x2048", "4096x4096"}

func TestOtohaGeminiImageCatalogPriceEqualsTheCharge(t *testing.T) {
	f := newGeminiImageBillingFixture(t, nil)
	for _, model := range []string{"gemini-3.1-flash-image", "gemini-3-pro-image"} {
		_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry(model, geminiImageSizes...))
		require.NoError(t, err)
		f.requireCatalogEqualsCharge(t, model)
	}
	catalog, err := f.catalog.CatalogForGroup(context.Background(), f.group.ID)
	require.NoError(t, err)
	sizes := catalog.ImageModels[0].Sizes
	require.InDelta(t, 0.1008, sizes[0].Price, 1e-6, "0.0672 for 1K, group rate 1.5")
	require.InDelta(t, 0.1512, sizes[1].Price, 1e-6, "billing charges 1.5 times the 1K price for 2K")
	require.InDelta(t, 0.2016, sizes[2].Price, 1e-6, "and twice for 4K")
}

func TestOtohaGeminiImageCatalogPriceEqualsTheChargeWithTheGroupsImagePrices(t *testing.T) {
	price1k, price2k := 0.05, 0.08
	f := newGeminiImageBillingFixture(t, func(g *Group) {
		g.ImagePrice1K, g.ImagePrice2K = &price1k, &price2k
	})
	_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry("gemini-3.1-flash-image", geminiImageSizes...))
	require.NoError(t, err)

	f.requireCatalogEqualsCharge(t, "gemini-3.1-flash-image")
}

func TestOtohaGeminiImagePriceSetOnTheModelIsWhatIsCharged(t *testing.T) {
	f := newGeminiImageBillingFixture(t, nil)
	input := imageEntry("gemini-3.1-flash-image", geminiImageSizes...)
	input.Price1K = otohaFloat(0.04)
	_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, input)
	require.NoError(t, err)

	f.requireCatalogEqualsCharge(t, "gemini-3.1-flash-image")
	require.InDelta(t, 0.06, f.charge(t, "gemini-3.1-flash-image", "1K"), 1e-6, "the admin's 0.04 times the group rate 1.5")
}

func TestOtohaGeminiImagePricerHasNoPriceForANameBillingDoesNotKnow(t *testing.T) {
	f := newGeminiImageBillingFixture(t, nil)
	pricer := otohaGeminiImagePricer{gateway: f.gateway}

	price, ok := pricer.ImageUnitPrice(context.Background(), f.group, "gemini-3.1-flash-image", "1K")
	require.True(t, ok)
	require.InDelta(t, 0.0672, price, 1e-9)

	_, ok = pricer.ImageUnitPrice(context.Background(), f.group, "nano-banana-pro-preview", "1K")
	require.False(t, ok, "billing would charge its built-in default for an unknown name, which is no price of the model's own")

	price1k := 0.05
	f.group.ImagePrice1K = &price1k
	price, ok = pricer.ImageUnitPrice(context.Background(), f.group, "nano-banana-pro-preview", "1K")
	require.True(t, ok, "a price the group set for the tier is a price of its own")
	require.InDelta(t, 0.05, price, 1e-9)
}

func TestOtohaGeminiImagePricerHonorsImageCountBilling(t *testing.T) {
	for _, source := range []string{"channel", "group"} {
		t.Run(source, func(t *testing.T) {
			f := newGeminiImageBillingFixture(t, func(g *Group) { g.Platform = PlatformComposite })
			const model = "gemini-3.1-flash-image"
			resolver := newOpenAITokenImageChannelPricingResolverForTest(t, f.group.ID, model)
			f.gateway.resolver = NewModelPricingResolver(resolver.channelService, f.gateway.billingService)
			if source == "group" {
				f.group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeToken,
					InputPrice: otohaFloat(3e-6), OutputPrice: otohaFloat(15e-6)}}
			}
			pricer := otohaGeminiImagePricer{gateway: f.gateway}
			_, ok := pricer.ImageUnitPrice(context.Background(), f.group, model, "1K")
			require.False(t, ok, "disabled switch preserves token billing")
			f.group.ImageCountBilling = true
			price, ok := pricer.ImageUnitPrice(context.Background(), f.group, model, "1K")
			require.True(t, ok)
			require.InDelta(t, 0.0672, price, 1e-12, "the synthetic request must use the image endpoint's price")
			f.group.ImagePrice1K = otohaFloat(0.05)
			price, ok = pricer.ImageUnitPrice(context.Background(), f.group, model, "1K")
			require.True(t, ok)
			require.InDelta(t, 0.05, price, 1e-12, "configured group image price still wins")
		})
	}
}

func TestOtohaGeminiImageCountBillingDoesNotInventUnknownModelPrices(t *testing.T) {
	f := newGeminiImageBillingFixture(t, func(g *Group) {
		g.Platform = PlatformComposite
		g.ImageCountBilling = true
	})
	const unknown = "nano-banana-pro-preview"
	pricer := otohaGeminiImagePricer{gateway: f.gateway}
	_, ok := pricer.ImageUnitPrice(context.Background(), f.group, unknown, "1K")
	require.False(t, ok)
	resolver := newOpenAITokenImageChannelPricingResolverForTest(t, f.group.ID, unknown)
	f.gateway.resolver = NewModelPricingResolver(resolver.channelService, f.gateway.billingService)
	_, ok = pricer.ImageUnitPrice(context.Background(), f.group, unknown, "1K")
	require.False(t, ok, "a token price is not a model's own image price")
	f.group.ImagePrice1K = otohaFloat(0.05)
	price, ok := pricer.ImageUnitPrice(context.Background(), f.group, unknown, "1K")
	require.True(t, ok, "a configured group image price is valid for an unknown model")
	require.InDelta(t, 0.05, price, 1e-12)
}

// ---- Veo ----

func TestOtohaMediaCandidatesOfAGeminiKeyListVeoAsAVideoModel(t *testing.T) {
	got := mediaCandidates(OtohaAccountListing{
		Account: geminiKeyAccount(12, "Gemini AI Studio", nil),
		Models:  parseOtohaListedModels([]byte(geminiModelListBody), false),
	})

	require.Len(t, got, 1)
	veo := got["video/veo-3.1-generate-preview"]
	require.Equal(t, PlatformGemini, veo.Platform)
	require.Equal(t, OtohaImageEvidenceDeclared, veo.Evidence)
	require.Equal(t, "supported_generation_methods", veo.DeclaredBy)
	require.Equal(t, "Veo 3.1", veo.Name)
}

func TestOtohaDiscoverMediaDoesNotAddVeoBecauseTheGatewayHasNoEndpointForIt(t *testing.T) {
	f := newOtohaMediaFixture(OtohaAccountListing{
		Account: geminiKeyAccount(12, "Gemini AI Studio", nil),
		Models:  parseOtohaListedModels([]byte(geminiModelListBody), false),
	})

	report := f.discover(t)

	veo := resultsByKey(report)["video/veo-3.1-generate-preview"]
	require.Equal(t, OtohaImageResultNotAdded, veo.Result)
	require.Equal(t, OtohaImageReasonGatewayNotSupported, veo.Reason, "predictLongRunning is not forwarded, and billing has no price for a Veo second")
	require.Empty(t, f.stored(t))
	require.Equal(t, 1, report.Accounts[0].MediaModels)
}
