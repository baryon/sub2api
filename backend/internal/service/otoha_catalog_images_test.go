//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- fakes ----

type fakeOtohaImageRepo struct {
	mu      sync.Mutex
	nextID  int64
	entries map[int64]OtohaImageEntry
	lists   int
}

func newFakeOtohaImageRepo() *fakeOtohaImageRepo {
	return &fakeOtohaImageRepo{entries: map[int64]OtohaImageEntry{}}
}

func (r *fakeOtohaImageRepo) ListImagesByGroup(_ context.Context, groupID int64) ([]OtohaImageEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	out := []OtohaImageEntry{}
	for _, e := range r.entries {
		if e.GroupID == groupID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *fakeOtohaImageRepo) CreateImage(_ context.Context, entry *OtohaImageEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.GroupID == entry.GroupID && e.ModelID == entry.ModelID {
			return ErrOtohaCatalogEntryExists
		}
	}
	r.nextID++
	entry.ID = r.nextID
	r.entries[entry.ID] = *entry
	return nil
}

func (r *fakeOtohaImageRepo) UpdateImage(_ context.Context, entry *OtohaImageEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[entry.ID]; !ok {
		return ErrOtohaCatalogEntryNotFound
	}
	r.entries[entry.ID] = *entry
	return nil
}

func (r *fakeOtohaImageRepo) DeleteImage(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return ErrOtohaCatalogEntryNotFound
	}
	delete(r.entries, id)
	return nil
}

// fakeOtohaImagePricer gives the price of one image in a size tier, before any rate, per model.
type fakeOtohaImagePricer map[string]map[string]float64

func (p fakeOtohaImagePricer) ImageUnitPrice(_ context.Context, _ *Group, modelID, tier string) (float64, bool) {
	price, ok := p[modelID][tier]
	return price, ok
}

type fakeOtohaImageRouting map[string]string

func (r fakeOtohaImageRouting) ImageRoute(_ context.Context, _ *Group, modelID string) OtohaModelRoute {
	if problem, ok := r[modelID]; ok {
		return OtohaModelRoute{Problem: problem}
	}
	return OtohaModelRoute{}
}

type otohaImageFixture struct {
	*otohaCatalogFixture
	images  *fakeOtohaImageRepo
	pricer  fakeOtohaImagePricer
	routing fakeOtohaImageRouting
}

func newOtohaImageFixture() *otohaImageFixture {
	f := &otohaImageFixture{
		otohaCatalogFixture: newOtohaCatalogFixture(),
		images:              newFakeOtohaImageRepo(),
		pricer: fakeOtohaImagePricer{
			"gpt-image-2":        {"1K": 0.04, "2K": 0.06, "4K": 0.12},
			"grok-imagine-image": {"1K": 0.02, "2K": 0.02, "4K": 0.02},
		},
		routing: fakeOtohaImageRouting{},
	}
	f.groups[otohaGroupID].AllowImageGeneration = true
	f.svc.WithImages(f.images, f.pricer, f.routing)
	return f
}

func (f *otohaImageFixture) addImage(t *testing.T, input OtohaImageEntryInput) *OtohaImageEntry {
	t.Helper()
	entry, err := f.svc.CreateImageEntry(context.Background(), otohaGroupID, input)
	require.NoError(t, err)
	return entry
}

func gptImageInput() OtohaImageEntryInput {
	return OtohaImageEntryInput{
		ModelID: "gpt-image-2", Name: "GPT Image 2", Enabled: true,
		Sizes:     []string{"1024x1024", "1536x1024", "3840x2160"},
		Qualities: []string{"low", "medium", "high"},
		Edits:     true,
	}
}

// ---- the catalog the app reads ----

func TestOtohaImageModelsAreInTheirOwnListNotAmongTheChatModels(t *testing.T) {
	f := newOtohaImageFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	f.addImage(t, gptImageInput())

	catalog := f.catalog(t, otohaGroupID)
	require.Equal(t, []string{"gpt-6-luna"}, otohaModelIDs(catalog), "an older app, which reads only models, never sees an image model")
	require.Len(t, catalog.ImageModels, 1)
	require.Equal(t, "gpt-image-2", catalog.ImageModels[0].ID)
}

func TestOtohaImageModelJSONShape(t *testing.T) {
	f := newOtohaImageFixture()
	f.groups[otohaGroupID].RateMultiplier = 1.5
	f.addImage(t, gptImageInput())

	body, err := json.Marshal(f.catalog(t, otohaGroupID).ImageModels[0])
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "gpt-image-2",
		"name": "GPT Image 2",
		"api": "openai-images",
		"endpoint": "POST /v1/images/generations",
		"app": true,
		"edits": true,
		"qualities": ["low", "medium", "high"],
		"currency": "USD",
		"per": "image",
		"sizes": [
			{"size": "1024x1024", "tier": "1K", "price": 0.06},
			{"size": "1536x1024", "tier": "2K", "price": 0.09},
			{"size": "3840x2160", "tier": "4K", "price": 0.18}
		]
	}`, string(body))
}

func TestOtohaCatalogWithOnlyImageModelsStillHasACatalog(t *testing.T) {
	f := newOtohaImageFixture()
	f.addImage(t, gptImageInput())

	catalog := f.catalog(t, otohaGroupID)
	require.NotNil(t, catalog, "the group has an image model, so the app reads its catalog")
	require.Empty(t, catalog.Models)
	body, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.Contains(t, string(body), `"models":[]`)
	require.Contains(t, string(body), `"imageModels":[{`)
}

func TestOtohaCatalogWithoutImageModelsHasAnEmptyList(t *testing.T) {
	f := newOtohaImageFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	body, err := json.Marshal(f.catalog(t, otohaGroupID))
	require.NoError(t, err)
	require.Contains(t, string(body), `"imageModels":[]`)
}

func TestOtohaImagePriceIsBillingsPriceTimesTheImageRate(t *testing.T) {
	f := newOtohaImageFixture()
	f.groups[otohaGroupID].RateMultiplier = 2
	f.addImage(t, gptImageInput())

	model := f.catalog(t, otohaGroupID).ImageModels[0]
	require.Equal(t, 0.08, model.Sizes[0].Price, "1K: 0.04 before the rate, rate 2")
	require.Equal(t, 0.12, model.Sizes[1].Price, "1536x1024 is billed as 2K")

	owned, err := f.svc.CatalogForGroupAtRate(context.Background(), otohaGroupID, 3)
	require.NoError(t, err)
	require.Equal(t, 0.12, owned.ImageModels[0].Sizes[0].Price, "a key owner's own rate replaces the group's")

	f.groups[otohaGroupID].ImageRateIndependent = true
	f.groups[otohaGroupID].ImageRateMultiplier = 0.5
	f.svc.InvalidateGroup(otohaGroupID)
	independent, err := f.svc.CatalogForGroupAtRate(context.Background(), otohaGroupID, 3)
	require.NoError(t, err)
	require.Equal(t, 0.02, independent.ImageModels[0].Sizes[0].Price, "a group with its own image rate charges images at that rate")
}

func TestOtohaImagePriceSetOnTheEntryReplacesBillingsPriceInThatTier(t *testing.T) {
	f := newOtohaImageFixture()
	input := gptImageInput()
	input.Price1K = otohaFloat(0.05)
	f.addImage(t, input)

	sizes := f.catalog(t, otohaGroupID).ImageModels[0].Sizes
	require.Equal(t, 0.05, sizes[0].Price, "1K is the admin's")
	require.Equal(t, 0.06, sizes[1].Price, "2K follows billing")
	require.Equal(t, 0.12, sizes[2].Price, "4K follows billing")
}

func TestOtohaImageModelLeftOutWhenItCannotBeServedOrPriced(t *testing.T) {
	f := newOtohaImageFixture()
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-image-2", "grok-imagine-image", "no-price", "no-account"}}
	f.pricer["no-account"] = map[string]float64{"1K": 0.1, "2K": 0.1, "4K": 0.1}
	f.routing["no-account"] = OtohaCatalogProblemNoAccount
	for _, id := range []string{"gpt-image-2", "grok-imagine-image", "no-price", "no-account", "outside-allowlist"} {
		input := gptImageInput()
		input.ModelID, input.Name = id, id
		f.addImage(t, input)
	}
	f.pricer["outside-allowlist"] = map[string]float64{"1K": 0.1, "2K": 0.1, "4K": 0.1}

	got := []string{}
	for _, m := range f.catalog(t, otohaGroupID).ImageModels {
		got = append(got, m.ID)
	}
	require.Equal(t, []string{"gpt-image-2", "grok-imagine-image"}, got)

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	problems := map[string]string{}
	for _, e := range view.ImageEntries {
		problems[e.ModelID] = e.Problem
	}
	require.Equal(t, map[string]string{
		"gpt-image-2":        "",
		"grok-imagine-image": "",
		"no-price":           OtohaCatalogProblemNoPrice,
		"no-account":         OtohaCatalogProblemNoAccount,
		"outside-allowlist":  OtohaCatalogProblemNotAllowed,
	}, problems)
}

func TestOtohaImageModelNeedsTheGroupToAllowImageGeneration(t *testing.T) {
	f := newOtohaImageFixture()
	f.groups[otohaGroupID].AllowImageGeneration = false
	f.addImage(t, gptImageInput())

	require.Empty(t, f.catalog(t, otohaGroupID).ImageModels)
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaCatalogProblemImagesNotAllowed, view.ImageEntries[0].Problem)
}

func TestOtohaDisabledImageModelIsLeftOut(t *testing.T) {
	f := newOtohaImageFixture()
	input := gptImageInput()
	input.Enabled = false
	f.addImage(t, input)

	require.Empty(t, f.catalog(t, otohaGroupID).ImageModels)
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaCatalogProblemDisabled, view.ImageEntries[0].Problem)
}

func TestOtohaAdminViewShowsEveryTierPriceBeforeAndAfterTheRate(t *testing.T) {
	f := newOtohaImageFixture()
	f.groups[otohaGroupID].RateMultiplier = 2
	input := gptImageInput()
	input.Price2K = otohaFloat(0.07)
	f.addImage(t, input)

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, []OtohaImageTierPrice{
		{Tier: "1K", Upstream: 0.04, Sale: 0.08},
		{Tier: "2K", Upstream: 0.07, Sale: 0.14, Set: true},
		{Tier: "4K", Upstream: 0.12, Sale: 0.24},
	}, view.ImageEntries[0].Prices)
	require.Len(t, view.Preview.ImageModels, 1, "the preview is exactly what the app receives")
}

func TestOtohaImageRevisionChangesWithAPriceButCatalogsWithoutImagesKeepTheirs(t *testing.T) {
	f := newOtohaImageFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	without := f.catalog(t, otohaGroupID).Revision

	// A catalog with no image model hashes what it did before image models existed.
	catalog := f.catalog(t, otohaGroupID)
	legacy, err := json.Marshal(struct {
		Schema int                 `json:"schema"`
		Models []OtohaCatalogModel `json:"models"`
	}{catalog.Schema, catalog.Models})
	require.NoError(t, err)
	require.Equal(t, hashOtohaRevision(legacy), without)

	entry := f.addImage(t, gptImageInput())
	first := f.catalog(t, otohaGroupID).Revision
	require.NotEqual(t, without, first)

	input := gptImageInput()
	input.Price1K = otohaFloat(0.05)
	_, err = f.svc.UpdateImageEntry(context.Background(), otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	require.NotEqual(t, first, f.catalog(t, otohaGroupID).Revision)
}

// ---- editing ----

func TestOtohaImageEntryIsChecked(t *testing.T) {
	f := newOtohaImageFixture()
	ctx := context.Background()
	bad := map[string]func(*OtohaImageEntryInput){
		"no model":            func(in *OtohaImageEntryInput) { in.ModelID = "" },
		"model with a space":  func(in *OtohaImageEntryInput) { in.ModelID = "gpt image" },
		"no size":             func(in *OtohaImageEntryInput) { in.Sizes = nil },
		"a size not WxH":      func(in *OtohaImageEntryInput) { in.Sizes = []string{"large"} },
		"a size of zero":      func(in *OtohaImageEntryInput) { in.Sizes = []string{"0x1024"} },
		"too many sizes":      func(in *OtohaImageEntryInput) { in.Sizes = manySizes(13) },
		"a quality with caps": func(in *OtohaImageEntryInput) { in.Qualities = []string{"Very High"} },
		"negative price":      func(in *OtohaImageEntryInput) { in.Price1K = otohaFloat(-1) },
		"huge price":          func(in *OtohaImageEntryInput) { in.Price4K = otohaFloat(1e9) },
	}
	for name, mutate := range bad {
		input := gptImageInput()
		mutate(&input)
		_, err := f.svc.CreateImageEntry(ctx, otohaGroupID, input)
		require.Error(t, err, name)
	}
	require.Empty(t, f.images.entries)
}

func manySizes(n int) []string {
	sizes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		sizes = append(sizes, fmt.Sprintf("1024x%d", 1000+i*16))
	}
	return sizes
}

func TestOtohaImageEntryIsNormalized(t *testing.T) {
	f := newOtohaImageFixture()
	entry := f.addImage(t, OtohaImageEntryInput{
		ModelID: " gpt-image-2 ", Enabled: true,
		Sizes:     []string{" 1024X1024 ", "1024x1024", "1536x1024"},
		Qualities: []string{"Low", "low", "HIGH"},
	})
	require.Equal(t, "gpt-image-2", entry.ModelID)
	require.Equal(t, "gpt-image-2", entry.Name, "the name defaults to the model")
	require.Equal(t, []string{"1024x1024", "1536x1024"}, entry.Sizes)
	require.Equal(t, []string{"low", "high"}, entry.Qualities)
}

func TestOtohaImageEntryCanBeEditedAndRemoved(t *testing.T) {
	f := newOtohaImageFixture()
	ctx := context.Background()
	entry := f.addImage(t, gptImageInput())

	_, err := f.svc.CreateImageEntry(ctx, otohaGroupID, gptImageInput())
	require.ErrorIs(t, err, ErrOtohaCatalogEntryExists, "a model is listed once")

	input := gptImageInput()
	input.Sizes = []string{"1024x1024"}
	input.Edits = false
	updated, err := f.svc.UpdateImageEntry(ctx, otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	require.Equal(t, []string{"1024x1024"}, updated.Sizes)
	model := f.catalog(t, otohaGroupID).ImageModels[0]
	require.Len(t, model.Sizes, 1)
	require.False(t, model.Edits)

	_, err = f.svc.UpdateImageEntry(ctx, otohaGroupID+1, entry.ID, input)
	require.ErrorIs(t, err, ErrOtohaCatalogEntryNotFound, "another group's entry cannot be edited")
	require.ErrorIs(t, f.svc.DeleteImageEntry(ctx, otohaGroupID+1, entry.ID), ErrOtohaCatalogEntryNotFound)

	require.NoError(t, f.svc.DeleteImageEntry(ctx, otohaGroupID, entry.ID))
	require.Nil(t, f.catalog(t, otohaGroupID), "nothing left in the catalog")
}

// ---- what billing reads ----

func TestOtohaImagePriceOverridesAreWhatTheAdminSet(t *testing.T) {
	f := newOtohaImageFixture()
	input := gptImageInput()
	input.Price1K = otohaFloat(0.05)
	input.Price4K = otohaFloat(0.2)
	f.addImage(t, input)
	f.addImage(t, OtohaImageEntryInput{ModelID: "grok-imagine-image", Enabled: true, Sizes: []string{"1024x1024"}})

	prices, ok := f.svc.OtohaImagePriceOverrides(context.Background(), otohaGroupID, "gpt-image-2")
	require.True(t, ok)
	require.Equal(t, &OtohaImageTierPrices{P1K: otohaFloat(0.05), P4K: otohaFloat(0.2)}, &prices)

	_, ok = f.svc.OtohaImagePriceOverrides(context.Background(), otohaGroupID, "grok-imagine-image")
	require.False(t, ok, "an entry that sets no price leaves billing as it is")
	_, ok = f.svc.OtohaImagePriceOverrides(context.Background(), otohaGroupID, "unknown")
	require.False(t, ok)
	_, ok = f.svc.OtohaImagePriceOverrides(context.Background(), 8, "gpt-image-2")
	require.False(t, ok, "prices are per group")
}

func TestOtohaImagePriceOverridesAreKeptBriefAndDroppedOnEdit(t *testing.T) {
	f := newOtohaImageFixture()
	input := gptImageInput()
	input.Price1K = otohaFloat(0.05)
	entry := f.addImage(t, input)
	ctx := context.Background()
	reads := f.images.lists

	for i := 0; i < 5; i++ {
		_, ok := f.svc.OtohaImagePriceOverrides(ctx, otohaGroupID, "gpt-image-2")
		require.True(t, ok)
	}
	require.Equal(t, reads+1, f.images.lists, "billing does not read the store for every image")

	input.Price1K = otohaFloat(0.09)
	_, err := f.svc.UpdateImageEntry(ctx, otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	prices, ok := f.svc.OtohaImagePriceOverrides(ctx, otohaGroupID, "gpt-image-2")
	require.True(t, ok)
	require.Equal(t, 0.09, *prices.P1K, "an edit shows in billing at once")
}
