//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeOtohaTierPricer prices like fakeOtohaPricer and knows the tiers above the base price.
type fakeOtohaTierPricer struct {
	fakeOtohaPricer
	tiers map[string][]OtohaPriceTier
}

func (p fakeOtohaTierPricer) UpstreamLongerTiers(_ context.Context, _ *Group, modelID string) []OtohaPriceTier {
	return p.tiers[modelID]
}

func newOtohaTierFixture(tiers map[string][]OtohaPriceTier) *otohaCatalogFixture {
	f := newOtohaCatalogFixture()
	pricer := fakeOtohaTierPricer{fakeOtohaPricer: f.pricer, tiers: tiers}
	f.svc = NewOtohaCatalogServiceWithDeps(f.repo, f.groups, pricer, f.metadata, f.routing)
	return f
}

func otohaModelByID(t *testing.T, catalog *OtohaCatalog, id string) OtohaCatalogModel {
	t.Helper()
	require.NotNil(t, catalog)
	for _, m := range catalog.Models {
		if m.ID == id {
			return m
		}
	}
	require.FailNow(t, "model not in the catalog", id)
	return OtohaCatalogModel{}
}

func TestOtohaPriceTiersOfAModelWithoutLongerTiersIsTheBasePrice(t *testing.T) {
	f := newOtohaTierFixture(nil)
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "gpt-6-luna")
	require.Equal(t, []OtohaPriceTier{{Input: 2.5, Output: 10, CachedInput: otohaFloat(0.25)}}, model.PriceTiers)
	require.Equal(t, model.Price.Input, model.PriceTiers[0].Input, "the base tier is the old price field")
	require.Equal(t, OtohaCompaction{Kind: "none"}, model.Compaction)
}

func TestOtohaPriceTiersWithAPricerThatKnowsNoTiersIsTheBasePrice(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "deepseek-v4")
	require.Equal(t, []OtohaPriceTier{{Input: 0.27, Output: 1.1}}, model.PriceTiers)
}

func TestOtohaPriceTiersAddAStrictlyLongerTier(t *testing.T) {
	f := newOtohaTierFixture(map[string][]OtohaPriceTier{
		"claude-opus": {{AboveInputTokens: 100000, Input: 75, Output: 375, CachedInput: otohaFloat(7.5)}},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus")
	require.Equal(t, []OtohaPriceTier{
		{AboveInputTokens: 0, Input: 15, Output: 75},
		{AboveInputTokens: 100000, Inclusive: false, Input: 75, Output: 375, CachedInput: otohaFloat(7.5)},
	}, model.PriceTiers)
	require.Equal(t, 15.0, model.Price.Input, "price stays the base price for older apps")
}

func TestOtohaPriceTiersKeepAnInclusiveThreshold(t *testing.T) {
	f := newOtohaTierFixture(map[string][]OtohaPriceTier{
		"gpt-6-luna": {{AboveInputTokens: 200000, Inclusive: true, Input: 5, Output: 20}},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "gpt-6-luna")
	require.Len(t, model.PriceTiers, 2)
	require.Equal(t, OtohaPriceTier{AboveInputTokens: 200000, Inclusive: true, Input: 5, Output: 20}, model.PriceTiers[1])
}

func TestOtohaPriceTiersAreAtTheGroupRate(t *testing.T) {
	f := newOtohaTierFixture(map[string][]OtohaPriceTier{
		"claude-opus": {{AboveInputTokens: 100000, Input: 75, Output: 375, CachedInput: otohaFloat(7.5)}},
	})
	f.groups[otohaGroupID].RateMultiplier = 0.5
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus")
	require.Equal(t, []OtohaPriceTier{
		{Input: 7.5, Output: 37.5},
		{AboveInputTokens: 100000, Input: 37.5, Output: 187.5, CachedInput: otohaFloat(3.75)},
	}, model.PriceTiers)

	// A key owner's own rate prices the tiers too.
	owned, err := f.svc.CatalogForGroupAtRate(context.Background(), otohaGroupID, 2)
	require.NoError(t, err)
	tiers := otohaModelByID(t, owned, "claude-opus").PriceTiers
	require.Equal(t, 30.0, tiers[0].Input)
	require.Equal(t, 150.0, tiers[1].Input)
}

func TestOtohaPriceTiersTheAdminSetReplaceTheDerivedOnes(t *testing.T) {
	f := newOtohaTierFixture(map[string][]OtohaPriceTier{
		"claude-opus": {{AboveInputTokens: 100000, Input: 75, Output: 375}},
	})
	f.groups[otohaGroupID].RateMultiplier = 2
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "claude-opus", Enabled: true,
		PriceTiers: []OtohaPriceTier{
			{Input: 30, Output: 150},
			{AboveInputTokens: 128000, Inclusive: true, Input: 60, Output: 300, CachedInput: otohaFloat(6)},
		},
	})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus")
	require.Equal(t, []OtohaPriceTier{
		{Input: 30, Output: 150},
		{AboveInputTokens: 128000, Inclusive: true, Input: 60, Output: 300, CachedInput: otohaFloat(6)},
	}, model.PriceTiers, "the admin's tiers are the sale prices as they are")

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, model.PriceTiers, view.Entries[0].EffectivePriceTiers)
	require.Len(t, view.Entries[0].PriceTiers, 2)
}

func TestOtohaAdminViewShowsTheDerivedTiersWhenNoneAreSet(t *testing.T) {
	f := newOtohaTierFixture(map[string][]OtohaPriceTier{
		"claude-opus": {{AboveInputTokens: 100000, Input: 75, Output: 375}},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Empty(t, view.Entries[0].PriceTiers)
	require.Len(t, view.Entries[0].EffectivePriceTiers, 2)
}

func TestOtohaPriceTiersInputIsChecked(t *testing.T) {
	f := newOtohaCatalogFixture()
	cases := map[string][]OtohaPriceTier{
		"first tier not the base":   {{AboveInputTokens: 1000, Input: 1, Output: 2}},
		"first tier inclusive":      {{Inclusive: true, Input: 1, Output: 2}},
		"not rising":                {{Input: 1, Output: 2}, {AboveInputTokens: 5000, Input: 2, Output: 3}, {AboveInputTokens: 5000, Input: 3, Output: 4}},
		"negative price":            {{Input: -1, Output: 2}},
		"negative bound":            {{Input: 1, Output: 2}, {AboveInputTokens: -5, Input: 2, Output: 3}},
		"too many tiers":            make([]OtohaPriceTier, otohaPriceTiersMax+1),
		"cached price out of range": {{Input: 1, Output: 2, CachedInput: otohaFloat(-0.1)}},
	}
	for name, tiers := range cases {
		_, err := f.svc.CreateEntry(context.Background(), otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true, PriceTiers: tiers})
		require.Error(t, err, name)
	}
}

func TestOtohaCompactionIsStoredAndReported(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true, Compaction: OtohaCompaction{Kind: "Anthropic", MinTrigger: 50000}})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true, Compaction: OtohaCompaction{Kind: "openai"}})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true, Compaction: OtohaCompaction{Kind: "none", MinTrigger: 9}})

	catalog := f.catalog(t, otohaGroupID)
	require.Equal(t, OtohaCompaction{Kind: "anthropic", MinTrigger: 50000}, otohaModelByID(t, catalog, "claude-opus").Compaction)
	require.Equal(t, OtohaCompaction{Kind: "openai"}, otohaModelByID(t, catalog, "gpt-6-luna").Compaction)
	require.Equal(t, OtohaCompaction{Kind: "none"}, otohaModelByID(t, catalog, "deepseek-v4").Compaction, "a trigger means nothing without a kind")

	_, err := f.svc.CreateEntry(context.Background(), 8, OtohaCatalogEntryInput{ModelID: "x", Enabled: true, Compaction: OtohaCompaction{Kind: "gemini"}})
	require.Error(t, err)
	_, err = f.svc.CreateEntry(context.Background(), 8, OtohaCatalogEntryInput{ModelID: "x", Enabled: true, Compaction: OtohaCompaction{Kind: "openai", MinTrigger: -1}})
	require.Error(t, err)
}

func TestOtohaRevisionChangesWithTiersAndCompaction(t *testing.T) {
	f := newOtohaCatalogFixture()
	entry := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})
	first := f.catalog(t, otohaGroupID).Revision

	input := OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true, Compaction: OtohaCompaction{Kind: "anthropic", MinTrigger: 50000}}
	_, err := f.svc.UpdateEntry(context.Background(), otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	second := f.catalog(t, otohaGroupID).Revision
	require.NotEqual(t, first, second)

	input.PriceTiers = []OtohaPriceTier{{Input: 15, Output: 75}, {AboveInputTokens: 100000, Input: 75, Output: 375}}
	_, err = f.svc.UpdateEntry(context.Background(), otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	require.NotEqual(t, second, f.catalog(t, otohaGroupID).Revision)
}

func TestOtohaCatalogJSONShapeOfTheNewFields(t *testing.T) {
	f := newOtohaTierFixture(map[string][]OtohaPriceTier{
		"claude-opus": {{AboveInputTokens: 100000, Input: 75, Output: 375, CachedInput: otohaFloat(7.5)}},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true, Compaction: OtohaCompaction{Kind: "anthropic", MinTrigger: 50000}})

	body, err := json.Marshal(otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus"))
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &got))
	require.JSONEq(t, `[{"aboveInputTokens":0,"inclusive":false,"input":15,"output":75},{"aboveInputTokens":100000,"inclusive":false,"input":75,"output":375,"cachedInput":7.5}]`, string(got["priceTiers"]))
	require.JSONEq(t, `{"kind":"anthropic","minTrigger":50000}`, string(got["compaction"]))
	require.Contains(t, got, "price", "older apps still read price")
}

// The tiers come from the same long-context rules billing charges by.
func TestOtohaResolverPricerGivesBillingsLongContextTiers(t *testing.T) {
	pricer := otohaResolverPricer{resolver: otohaTestResolver()}
	group := &Group{ID: 3, Platform: PlatformOpenAI, LongContextPricingEnabled: true}

	strict := pricer.UpstreamLongerTiers(context.Background(), group, "gpt-6-astra")
	require.Len(t, strict, 1)
	require.Equal(t, 272000, strict[0].AboveInputTokens)
	require.False(t, strict[0].Inclusive)
	require.InDelta(t, 20.0, strict[0].Input, 1e-6, "input is doubled above 272,000 tokens")
	require.InDelta(t, 75.0, strict[0].Output, 1e-6, "output is 1.5 times")
	require.NotNil(t, strict[0].CachedInput)
	require.InDelta(t, 2.0, *strict[0].CachedInput, 1e-6, "cached input follows the input multiplier")

	inclusive := pricer.UpstreamLongerTiers(context.Background(), group, "grok-4.5")
	require.Len(t, inclusive, 1)
	require.Equal(t, 200000, inclusive[0].AboveInputTokens)
	require.True(t, inclusive[0].Inclusive, "billing applies the xAI tier at the threshold itself")
	require.InDelta(t, 4.0, inclusive[0].Input, 1e-6)
	require.InDelta(t, 12.0, inclusive[0].Output, 1e-6)
}

func TestOtohaResolverPricerOfAModelWithoutALongerTierGivesNone(t *testing.T) {
	pricer := otohaResolverPricer{resolver: otohaTestResolver()}
	group := &Group{ID: 3, Platform: PlatformAnthropic, LongContextPricingEnabled: true}
	require.Empty(t, pricer.UpstreamLongerTiers(context.Background(), group, "claude-sonnet-4-5"))
	require.Empty(t, otohaResolverPricer{}.UpstreamLongerTiers(context.Background(), group, "gpt-6-astra"))

	off := &Group{ID: 3, Platform: PlatformOpenAI}
	require.Empty(t, pricer.UpstreamLongerTiers(context.Background(), off, "gpt-6-astra"), "a group that does not bill by prompt length has one price")
}
