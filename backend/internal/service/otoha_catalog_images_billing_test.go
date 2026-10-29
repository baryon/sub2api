//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The price the app is shown for an image must be the price the request is charged: for each model and size, the
// catalog's price equals the ActualCost that RecordUsage writes for one image of that size.

type otohaImageBillingFixture struct {
	usageRepo *openAIRecordUsageLogRepoStub
	userRepo  *openAIRecordUsageUserRepoStub
	openai    *OpenAIGatewayService
	group     *Group
	catalog   *OtohaCatalogService
}

func newOtohaImageBillingFixture(t *testing.T, mutate func(*Group)) *otohaImageBillingFixture {
	t.Helper()
	f := &otohaImageBillingFixture{
		usageRepo: &openAIRecordUsageLogRepoStub{inserted: true},
		userRepo:  &openAIRecordUsageUserRepoStub{},
	}
	f.openai = newOpenAIRecordUsageServiceForTest(f.usageRepo, f.userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	f.openai.resolver = NewModelPricingResolver(nil, f.openai.billingService)
	f.group = &Group{
		ID: otohaGroupID, Name: "Otoha", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
		RateMultiplier: 1.5, ImageRateMultiplier: 1, AllowImageGeneration: true,
	}
	if mutate != nil {
		mutate(f.group)
	}
	f.catalog = NewOtohaCatalogServiceWithDeps(
		newFakeOtohaCatalogRepo(), fakeOtohaCatalogGroups{otohaGroupID: f.group}, fakeOtohaPricer{}, fakeOtohaMetadata{}, fakeOtohaRouting{},
	).WithImages(newFakeOtohaImageRepo(), otohaOpenAIImagePricer{openai: f.openai}, nil)
	f.openai.SetOtohaImagePriceOverrides(f.catalog)
	return f
}

// charge records one image of a requested output size and returns what was charged.
func (f *otohaImageBillingFixture) charge(t *testing.T, model, outputSize string) float64 {
	t.Helper()
	groupID := f.group.ID
	err := f.openai.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "img-" + model + "-" + outputSize, Model: model, ImageCount: 1, ImageOutputSizes: []string{outputSize}, ImageEndpoint: true,
		},
		APIKey:  &APIKey{ID: 10, GroupID: &groupID, Group: f.group},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})
	require.NoError(t, err)
	require.NotNil(t, f.usageRepo.lastLog)
	require.Equal(t, 1, f.usageRepo.lastLog.ImageCount)
	require.Equal(t, string(BillingModeImage), *f.usageRepo.lastLog.BillingMode)
	return f.usageRepo.lastLog.ActualCost
}

func (f *otohaImageBillingFixture) requireCatalogEqualsCharge(t *testing.T, model string) {
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
	require.NotEmpty(t, shown.Sizes)
	for _, size := range shown.Sizes {
		charged := f.charge(t, model, size.Size)
		require.InDelta(t, size.Price, charged, 1e-6, "%s %s (%s): the app shows %v, the charge is %v", model, size.Size, size.Tier, size.Price, charged)
		require.Greater(t, size.Price, 0.0)
	}
}

func imageEntry(model string, sizes ...string) OtohaImageEntryInput {
	return OtohaImageEntryInput{ModelID: model, Enabled: true, Sizes: sizes, Qualities: []string{"medium"}}
}

var allImageSizes = []string{"1024x1024", "1536x1024", "3840x2160"}

func TestOtohaImageCatalogPriceEqualsTheChargeWithBillingsDefaults(t *testing.T) {
	f := newOtohaImageBillingFixture(t, nil)
	for _, model := range []string{"gpt-image-2", "grok-imagine-image"} {
		_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry(model, allImageSizes...))
		require.NoError(t, err)
		f.requireCatalogEqualsCharge(t, model)
	}
}

func TestOtohaImageCatalogPriceEqualsTheChargeWithTheGroupsImagePrices(t *testing.T) {
	f := newOtohaImageBillingFixture(t, func(g *Group) {
		g.ImagePrice1K, g.ImagePrice2K, g.ImagePrice4K = otohaFloat(0.04), otohaFloat(0.07), otohaFloat(0.15)
	})
	_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry("gpt-image-2", allImageSizes...))
	require.NoError(t, err)
	f.requireCatalogEqualsCharge(t, "gpt-image-2")

	catalog, _ := f.catalog.CatalogForGroup(context.Background(), f.group.ID)
	require.Equal(t, []float64{0.06, 0.105, 0.225}, []float64{
		catalog.ImageModels[0].Sizes[0].Price, catalog.ImageModels[0].Sizes[1].Price, catalog.ImageModels[0].Sizes[2].Price,
	}, "the group's 1K/2K/4K prices, times the rate of 1.5")
}

func TestOtohaImagePriceSetOnTheModelIsWhatIsCharged(t *testing.T) {
	f := newOtohaImageBillingFixture(t, func(g *Group) {
		g.ImagePrice1K = otohaFloat(0.04)
	})
	input := imageEntry("gpt-image-2", allImageSizes...)
	input.Price1K, input.Price4K = otohaFloat(0.05), otohaFloat(0.2)
	_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, input)
	require.NoError(t, err)
	_, err = f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry("grok-imagine-image", allImageSizes...))
	require.NoError(t, err)

	f.requireCatalogEqualsCharge(t, "gpt-image-2")
	f.requireCatalogEqualsCharge(t, "grok-imagine-image")

	require.InDelta(t, 0.05*1.5, f.charge(t, "gpt-image-2", "1024x1024"), 1e-9, "the model's price beats the group's 1K price")
	require.InDelta(t, 0.2*1.5, f.charge(t, "gpt-image-2", "3840x2160"), 1e-9)
	require.InDelta(t, 0.04*1.5, f.charge(t, "grok-imagine-image", "1024x1024"), 1e-9, "another model keeps billing's price: the group's 1K price")
}

func TestOtohaImagePriceEditShowsInTheNextCharge(t *testing.T) {
	f := newOtohaImageBillingFixture(t, nil)
	input := imageEntry("gpt-image-2", "1024x1024")
	input.Price1K = otohaFloat(0.05)
	entry, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, input)
	require.NoError(t, err)
	require.InDelta(t, 0.075, f.charge(t, "gpt-image-2", "1024x1024"), 1e-9)

	input.Price1K = otohaFloat(0.08)
	_, err = f.catalog.UpdateImageEntry(context.Background(), f.group.ID, entry.ID, input)
	require.NoError(t, err)
	require.InDelta(t, 0.12, f.charge(t, "gpt-image-2", "1024x1024"), 1e-9)
	f.requireCatalogEqualsCharge(t, "gpt-image-2")
}

func TestOtohaImageCatalogPriceEqualsTheChargeAtTheGroupsOwnImageRate(t *testing.T) {
	f := newOtohaImageBillingFixture(t, func(g *Group) {
		g.ImageRateIndependent, g.ImageRateMultiplier = true, 0.4
	})
	_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry("grok-imagine-image", allImageSizes...))
	require.NoError(t, err)
	f.requireCatalogEqualsCharge(t, "grok-imagine-image")
	require.InDelta(t, 0.02*0.4, f.charge(t, "grok-imagine-image", "1024x1024"), 1e-9)
}

func TestOtohaImageModelOfAGroupWhoseChannelChargesByTokenHasNoPerImagePrice(t *testing.T) {
	groupID := int64(77)
	resolver := newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gpt-image-2")

	openai := &OpenAIGatewayService{billingService: resolver.billingService, resolver: resolver}
	group := &Group{ID: groupID, Platform: PlatformOpenAI, Hydrated: true, RateMultiplier: 1, ImageRateMultiplier: 1}
	_, ok := otohaOpenAIImagePricer{openai: openai}.ImageUnitPrice(context.Background(), group, "gpt-image-2", "1K")
	require.False(t, ok, "billing charges that model by token, so there is no price per image to show")

	_, ok = otohaOpenAIImagePricer{openai: openai}.ImageUnitPrice(context.Background(), group, "grok-imagine-image", "1K")
	require.True(t, ok, "a model the channel does not price per token is still priced per image")
}

func TestOtohaOpenAIImagePricerHonorsImageCountBilling(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformComposite} {
		for _, source := range []string{"channel", "group"} {
			t.Run(platform+"/"+source, func(t *testing.T) {
				f := newOtohaImageBillingFixture(t, func(g *Group) {
					g.Platform = platform
					g.ImagePrice1K = otohaFloat(0.4)
				})
				const model = "gpt-image-2"
				f.openai.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, f.group.ID, model)
				if source == "group" {
					f.group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeToken,
						InputPrice: otohaFloat(3e-6), OutputPrice: otohaFloat(15e-6)}}
				}
				pricer := otohaOpenAIImagePricer{openai: f.openai}
				_, ok := pricer.ImageUnitPrice(context.Background(), f.group, model, "1K")
				require.False(t, ok, "disabled switch preserves token billing")
				f.group.ImageCountBilling = true
				price, ok := pricer.ImageUnitPrice(context.Background(), f.group, model, "1K")
				require.True(t, ok, "image endpoints now bill per image despite token pricing")
				require.InDelta(t, 0.4, price, 1e-12)
				_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, imageEntry(model, "1024x1024"))
				require.NoError(t, err, "the model must remain available in the directory")
				catalog, err := f.catalog.CatalogForGroup(context.Background(), f.group.ID)
				require.NoError(t, err)
				require.Len(t, catalog.ImageModels, 1)
				require.InDelta(t, 0.6, catalog.ImageModels[0].Sizes[0].Price, 1e-12, "group rate 1.5 applies to the shown price")
				require.InDelta(t, 0.6, f.charge(t, model, "1024x1024"), 1e-12, "the directory price equals the actual endpoint charge")
			})
		}
	}
}

func TestOtohaImageBillingHookIsAbsentWithoutACatalog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	groupID := int64(7)
	group := &Group{ID: groupID, Platform: PlatformOpenAI, Hydrated: true, RateMultiplier: 1, ImageRateMultiplier: 1, ImagePrice1K: otohaFloat(0.04)}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result:  &OpenAIForwardResult{RequestID: "r", Model: "gpt-image-2", ImageCount: 2, ImageOutputSizes: []string{"1024x1024", "1024x1024"}},
		APIKey:  &APIKey{ID: 10, GroupID: &groupID, Group: group},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})
	require.NoError(t, err)
	require.InDelta(t, 0.08, usageRepo.lastLog.ActualCost, 1e-9, "two images at the group's 1K price, as before")
}
