//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// ---- fakes ----

func cloneOtohaCatalogEntry(e OtohaCatalogEntry) OtohaCatalogEntry {
	out := e
	out.Inputs = append([]string(nil), e.Inputs...)
	out.Reasoning = append([]string(nil), e.Reasoning...)
	out.Roles = append([]string(nil), e.Roles...)
	out.Use = append([]string(nil), e.Use...)
	if e.Strengths != nil {
		out.Strengths = make(map[string]string, len(e.Strengths))
		for k, v := range e.Strengths {
			out.Strengths[k] = v
		}
	}
	return out
}

type fakeOtohaCatalogRepo struct {
	mu      sync.Mutex
	nextID  int64
	entries map[int64]OtohaCatalogEntry
}

func newFakeOtohaCatalogRepo() *fakeOtohaCatalogRepo {
	return &fakeOtohaCatalogRepo{entries: map[int64]OtohaCatalogEntry{}}
}

func (r *fakeOtohaCatalogRepo) ListByGroup(_ context.Context, groupID int64) ([]OtohaCatalogEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []OtohaCatalogEntry{}
	for _, e := range r.entries {
		if e.GroupID == groupID {
			out = append(out, cloneOtohaCatalogEntry(e))
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

func (r *fakeOtohaCatalogRepo) Create(_ context.Context, entry *OtohaCatalogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.GroupID == entry.GroupID && e.ModelID == entry.ModelID {
			return ErrOtohaCatalogEntryExists
		}
	}
	r.nextID++
	entry.ID = r.nextID
	r.entries[entry.ID] = cloneOtohaCatalogEntry(*entry)
	return nil
}

func (r *fakeOtohaCatalogRepo) Update(_ context.Context, entry *OtohaCatalogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[entry.ID]; !ok {
		return ErrOtohaCatalogEntryNotFound
	}
	for id, e := range r.entries {
		if id != entry.ID && e.GroupID == entry.GroupID && e.ModelID == entry.ModelID {
			return ErrOtohaCatalogEntryExists
		}
	}
	r.entries[entry.ID] = cloneOtohaCatalogEntry(*entry)
	return nil
}

func (r *fakeOtohaCatalogRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return ErrOtohaCatalogEntryNotFound
	}
	delete(r.entries, id)
	return nil
}

func (r *fakeOtohaCatalogRepo) UpdateSortOrders(_ context.Context, groupID int64, orders map[int64]int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, order := range orders {
		e, ok := r.entries[id]
		if !ok || e.GroupID != groupID {
			return ErrOtohaCatalogEntryNotFound
		}
		e.SortOrder = order
		r.entries[id] = e
	}
	return nil
}

type fakeOtohaCatalogGroups map[int64]*Group

func (g fakeOtohaCatalogGroups) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	group, ok := g[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	cp := *group
	return &cp, nil
}

type fakeOtohaPricer map[string]OtohaModelPrice

func (p fakeOtohaPricer) UpstreamPrice(_ context.Context, _ *Group, modelID string) (OtohaModelPrice, bool) {
	price, ok := p[modelID]
	return price, ok
}

type fakeOtohaRouting map[string]bool

func (r fakeOtohaRouting) CanRoute(_ context.Context, _ *Group, modelID string) bool {
	routable, known := r[modelID]
	return !known || routable
}

type fakeOtohaMetadata map[string]OtohaModelMetadata

func (m fakeOtohaMetadata) ModelMetadata(_ context.Context, _ *Group, modelID string) (OtohaModelMetadata, error) {
	md, ok := m[modelID]
	if !ok {
		return OtohaModelMetadata{}, errors.New("unknown model")
	}
	return md, nil
}

type otohaCatalogFixture struct {
	repo     *fakeOtohaCatalogRepo
	groups   fakeOtohaCatalogGroups
	pricer   fakeOtohaPricer
	routing  fakeOtohaRouting
	metadata fakeOtohaMetadata
	svc      *OtohaCatalogService
}

const otohaGroupID int64 = 7

func newOtohaCatalogFixture() *otohaCatalogFixture {
	f := &otohaCatalogFixture{
		repo: newFakeOtohaCatalogRepo(),
		groups: fakeOtohaCatalogGroups{
			otohaGroupID: {ID: otohaGroupID, Name: "Otoha", Platform: PlatformOpenAI, RateMultiplier: 1},
			8:            {ID: 8, Name: "Other", Platform: PlatformOpenAI, RateMultiplier: 1},
		},
		pricer: fakeOtohaPricer{
			"gpt-6-luna":  {Input: 2.5, Output: 10, CachedInput: otohaFloat(0.25)},
			"deepseek-v4": {Input: 0.27, Output: 1.1},
			"claude-opus": {Input: 15, Output: 75},
		},
		routing:  fakeOtohaRouting{},
		metadata: fakeOtohaMetadata{},
	}
	f.svc = NewOtohaCatalogServiceWithDeps(f.repo, f.groups, f.pricer, f.metadata, f.routing)
	return f
}

func otohaFloat(v float64) *float64 { return &v }

func (f *otohaCatalogFixture) add(t *testing.T, groupID int64, input OtohaCatalogEntryInput) *OtohaCatalogEntry {
	t.Helper()
	entry, err := f.svc.CreateEntry(context.Background(), groupID, input)
	require.NoError(t, err)
	return entry
}

func (f *otohaCatalogFixture) catalog(t *testing.T, groupID int64) *OtohaCatalog {
	t.Helper()
	catalog, err := f.svc.CatalogForGroup(context.Background(), groupID)
	require.NoError(t, err)
	return catalog
}

func otohaModelIDs(catalog *OtohaCatalog) []string {
	ids := []string{}
	for _, m := range catalog.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

// ---- the catalog the app reads ----

func TestOtohaCatalogOfAGroupWithoutEntriesIsNil(t *testing.T) {
	f := newOtohaCatalogFixture()
	require.Nil(t, f.catalog(t, otohaGroupID))
	require.Nil(t, f.catalog(t, 404), "an unknown group has no catalog either")
}

func TestOtohaCatalogListsOnlyEnabledModelsTheGroupCanServe(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-luna", "deepseek-v4", "claude-opus", "no-price-model"}}
	f.routing["claude-opus"] = false
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: false})
	f.pricer["grok-5"] = OtohaModelPrice{Input: 1, Output: 2}
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "grok-5", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "no-price-model", Enabled: true})

	catalog := f.catalog(t, otohaGroupID)
	require.NotNil(t, catalog)
	require.Equal(t, []string{"gpt-6-luna"}, otohaModelIDs(catalog),
		"disabled, outside the allowlist, without an account, and without a price are all left out")
}

func TestOtohaCatalogOfAGroupWithEveryModelDisabledIsEmptyNotNil(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: false})

	catalog := f.catalog(t, otohaGroupID)
	require.NotNil(t, catalog, "the group has a catalog; the admin turned its models off")
	require.Empty(t, catalog.Models)
	body, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.Contains(t, string(body), `"models":[]`)
}

func TestOtohaCatalogIsPerGroup(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	f.add(t, 8, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})

	require.Equal(t, []string{"gpt-6-luna"}, otohaModelIDs(f.catalog(t, otohaGroupID)))
	require.Equal(t, []string{"deepseek-v4"}, otohaModelIDs(f.catalog(t, 8)))
}

func TestOtohaCatalogFollowsTheAdminOrder(t *testing.T) {
	f := newOtohaCatalogFixture()
	a := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	b := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})
	c := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})
	require.Equal(t, []string{"gpt-6-luna", "deepseek-v4", "claude-opus"}, otohaModelIDs(f.catalog(t, otohaGroupID)), "new models go last")

	require.NoError(t, f.svc.ReorderEntries(context.Background(), otohaGroupID, []int64{c.ID, a.ID, b.ID}))
	require.Equal(t, []string{"claude-opus", "gpt-6-luna", "deepseek-v4"}, otohaModelIDs(f.catalog(t, otohaGroupID)))
}

func TestOtohaCatalogReorderMustNameExactlyTheGroupsEntries(t *testing.T) {
	f := newOtohaCatalogFixture()
	a := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	b := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})
	other := f.add(t, 8, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})
	ctx := context.Background()

	require.Error(t, f.svc.ReorderEntries(ctx, otohaGroupID, []int64{a.ID}), "missing one")
	require.Error(t, f.svc.ReorderEntries(ctx, otohaGroupID, []int64{a.ID, b.ID, other.ID}), "another group's entry")
	require.Error(t, f.svc.ReorderEntries(ctx, otohaGroupID, []int64{a.ID, a.ID}), "a repeat")
	require.Equal(t, []string{"claude-opus"}, otohaModelIDs(f.catalog(t, 8)))
}

func TestOtohaCatalogPriceIsTheUpstreamPriceTimesTheGroupRate(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.groups[otohaGroupID].RateMultiplier = 1.5
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	model := f.catalog(t, otohaGroupID).Models[0]
	require.Equal(t, "USD", model.Price.Currency)
	require.Equal(t, "1M tokens", model.Price.Per)
	require.Equal(t, 3.75, model.Price.Input)
	require.Equal(t, 15.0, model.Price.Output)
	require.NotNil(t, model.Price.CachedInput)
	require.Equal(t, 0.375, *model.Price.CachedInput)
}

func TestOtohaCatalogCostTierComesFromTheSalePrice(t *testing.T) {
	cases := []struct {
		input, output float64
		want          string
	}{
		{0.27, 1.1, OtohaCostLow},
		{2, 2.99, OtohaCostLow},
		{2, 3, OtohaCostStandard},
		{5, 24.99, OtohaCostStandard},
		{5, 25, OtohaCostHigh},
		{15, 75, OtohaCostHigh},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, OtohaCostTierForPrice(OtohaModelPrice{Input: tc.input, Output: tc.output}), "%v + %v", tc.input, tc.output)
	}

	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true, CostTier: OtohaCostStandard})
	models := f.catalog(t, otohaGroupID).Models
	require.Equal(t, OtohaCostLow, models[0].Cost)
	require.Equal(t, OtohaCostStandard, models[1].Cost, "the admin's tier wins")
}

func TestOtohaCatalogUseComesFromTheProfileUnlessTheAdminSetsIt(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "gpt-6-luna", Enabled: true, Speed: "fast", Complexity: "complex",
		Strengths: map[string]string{"coding": "strong", "research": "strong", "writing": "usable", "planning": "avoid"},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "deepseek-v4", Enabled: true, Strengths: map[string]string{"coding": "strong"}, Use: []string{"default", "summarize"},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})

	models := f.catalog(t, otohaGroupID).Models
	require.Equal(t, []string{"fast", "deep", "coding", "web"}, models[0].Use, "strong domains, complex work and speed, in the purchase design's order")
	require.Equal(t, []string{"default", "summarize"}, models[1].Use)
	require.Nil(t, models[2].Use, "no profile, no use")
}

func TestOtohaCatalogCarriesCapabilitiesAndProfile(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "gpt-6-luna", Name: "GPT-6 Luna", Description: "Everyday work", Enabled: true,
		Inputs: []string{"text", "image"}, Tools: true, Context: 1_050_000, MaxOutput: 128_000,
		Reasoning: []string{"low", "medium", "high"}, DefaultReasoning: "medium",
		Speed: "standard", Complexity: "complex", Roles: []string{"lead", "execute"}, ProfileSource: "vendor",
		Strengths: map[string]string{"planning": "strong"},
	})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})

	models := f.catalog(t, otohaGroupID).Models
	luna := models[0]
	require.Equal(t, "GPT-6 Luna", luna.Name)
	require.Equal(t, "Everyday work", luna.Description)
	require.Equal(t, []string{"text", "image"}, luna.Inputs)
	require.True(t, luna.Images)
	require.True(t, luna.Tools)
	require.Equal(t, 1_050_000, luna.Context)
	require.Equal(t, 128_000, luna.MaxOutput)
	require.Equal(t, []string{"low", "medium", "high"}, luna.Reasoning)
	require.Equal(t, "medium", luna.DefaultReasoning)
	require.Equal(t, "standard", luna.Speed)
	require.Equal(t, "complex", luna.Complexity)
	require.Equal(t, []string{"lead", "execute"}, luna.Roles)
	require.Equal(t, "vendor", luna.ProfileSource)
	require.Equal(t, map[string]string{"planning": "strong"}, luna.Strengths)

	plain := models[1]
	require.Equal(t, "deepseek-v4", plain.Name, "the name defaults to the model ID")
	require.Equal(t, []string{"text"}, plain.Inputs)
	require.False(t, plain.Images)
}

func TestOtohaCatalogJSONCarriesNoInternalFields(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "gpt-6-luna", Enabled: true, Inputs: []string{"text", "image"}, Tools: true, Context: 1000,
		MaxOutput: 100, Reasoning: []string{"low"}, DefaultReasoning: "low", Speed: "fast",
		Strengths: map[string]string{"coding": "strong"}, Complexity: "simple", Roles: []string{"execute"},
		Use: []string{"coding"}, ProfileSource: "admin", Description: "x", Name: "Luna",
	})
	body, err := json.Marshal(f.catalog(t, otohaGroupID))
	require.NoError(t, err)

	var decoded struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	allowed := map[string]bool{
		"id": true, "name": true, "description": true, "inputs": true, "images": true, "tools": true,
		"context": true, "maxOutput": true, "reasoning": true, "defaultReasoning": true, "cost": true,
		"price": true, "speed": true, "strengths": true, "complexity": true, "roles": true, "use": true,
		"profileSource": true,
	}
	for key := range decoded.Models[0] {
		require.True(t, allowed[key], "unexpected field %q in the app's catalog", key)
	}
	var price map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(decoded.Models[0]["price"], &price))
	for key := range price {
		require.Contains(t, []string{"currency", "per", "input", "output", "cachedInput"}, key)
	}
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &top))
	require.Len(t, top, 3)
	require.Contains(t, top, "schema")
	require.Contains(t, top, "revision")
	require.Contains(t, top, "models")
}

func TestOtohaCatalogRevisionChangesWhenTheCatalogOrAPriceChanges(t *testing.T) {
	f := newOtohaCatalogFixture()
	entry := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	first := f.catalog(t, otohaGroupID)
	require.NotEmpty(t, first.Revision)
	require.Equal(t, OtohaCatalogSchemaVersion, first.Schema)
	require.Equal(t, first.Revision, f.catalog(t, otohaGroupID).Revision, "same catalog, same revision")

	input := OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true, Description: "now with a description"}
	_, err := f.svc.UpdateEntry(context.Background(), otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	edited := f.catalog(t, otohaGroupID)
	require.NotEqual(t, first.Revision, edited.Revision, "an edit")

	f.groups[otohaGroupID].RateMultiplier = 2
	f.svc.InvalidateGroup(otohaGroupID)
	rated := f.catalog(t, otohaGroupID)
	require.NotEqual(t, edited.Revision, rated.Revision, "a group rate change moves the price")

	f.pricer["gpt-6-luna"] = OtohaModelPrice{Input: 3, Output: 12}
	f.svc.InvalidateGroup(otohaGroupID)
	repriced := f.catalog(t, otohaGroupID)
	require.NotEqual(t, rated.Revision, repriced.Revision, "an upstream price change")
}

func TestOtohaCatalogEditsShowAtOnce(t *testing.T) {
	f := newOtohaCatalogFixture()
	entry := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	require.Len(t, f.catalog(t, otohaGroupID).Models, 1)

	_, err := f.svc.UpdateEntry(context.Background(), otohaGroupID, entry.ID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: false})
	require.NoError(t, err)
	require.Empty(t, f.catalog(t, otohaGroupID).Models)

	require.NoError(t, f.svc.DeleteEntry(context.Background(), otohaGroupID, entry.ID))
	require.Nil(t, f.catalog(t, otohaGroupID))
}

// ---- editing ----

func requireOtohaBadRequest(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, 400, infraerrors.Code(err), "error: %v", err)
}

func TestOtohaCatalogEntryValidation(t *testing.T) {
	f := newOtohaCatalogFixture()
	ctx := context.Background()
	bad := []OtohaCatalogEntryInput{
		{ModelID: ""},
		{ModelID: "gpt 6"},
		{ModelID: "gpt-6", Speed: "warp"},
		{ModelID: "gpt-6", Complexity: "huge"},
		{ModelID: "gpt-6", Strengths: map[string]string{"cooking": "strong"}},
		{ModelID: "gpt-6", Strengths: map[string]string{"coding": "great"}},
		{ModelID: "gpt-6", Roles: []string{"boss"}},
		{ModelID: "gpt-6", Use: []string{"everything"}},
		{ModelID: "gpt-6", ProfileSource: "rumour"},
		{ModelID: "gpt-6", Inputs: []string{"smell"}},
		{ModelID: "gpt-6", Reasoning: []string{"low"}, DefaultReasoning: "high"},
		{ModelID: "gpt-6", Reasoning: []string{"Low Effort"}},
		{ModelID: "gpt-6", CostTier: "cheap"},
		{ModelID: "gpt-6", Context: -1},
		{ModelID: "gpt-6", MaxOutput: -1},
		{ModelID: "gpt-6", Context: 100_000_001},
		{ModelID: "gpt-6", MaxOutput: 100_000_001},
	}
	for _, input := range bad {
		_, err := f.svc.CreateEntry(ctx, otohaGroupID, input)
		requireOtohaBadRequest(t, err)
	}
	require.Nil(t, f.catalog(t, otohaGroupID), "nothing was saved")
}

func TestOtohaCatalogEntryIsNormalizedOnSave(t *testing.T) {
	f := newOtohaCatalogFixture()
	entry := f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "  gpt-6-luna ", Name: "  ", Speed: " Fast ", Strengths: map[string]string{" Coding ": " Strong "},
		Roles: []string{"execute", "lead", "execute"}, Reasoning: []string{"low", "high", "low"},
	})
	require.Equal(t, "gpt-6-luna", entry.ModelID)
	require.Equal(t, "gpt-6-luna", entry.Name)
	require.Equal(t, "fast", entry.Speed)
	require.Equal(t, map[string]string{"coding": "strong"}, entry.Strengths)
	require.Equal(t, []string{"lead", "execute"}, entry.Roles)
	require.Equal(t, []string{"low", "high"}, entry.Reasoning)
	require.Equal(t, []string{"text"}, entry.Inputs)
}

func TestOtohaCatalogModelAppearsOncePerGroup(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna"})
	_, err := f.svc.CreateEntry(context.Background(), otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna"})
	require.ErrorIs(t, err, ErrOtohaCatalogEntryExists)
	f.add(t, 8, OtohaCatalogEntryInput{ModelID: "gpt-6-luna"})
}

func TestOtohaCatalogEntriesBelongToTheirGroup(t *testing.T) {
	f := newOtohaCatalogFixture()
	ctx := context.Background()
	entry := f.add(t, 8, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	_, err := f.svc.UpdateEntry(ctx, otohaGroupID, entry.ID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna"})
	require.ErrorIs(t, err, ErrOtohaCatalogEntryNotFound)
	require.ErrorIs(t, f.svc.DeleteEntry(ctx, otohaGroupID, entry.ID), ErrOtohaCatalogEntryNotFound)
	require.Equal(t, []string{"gpt-6-luna"}, otohaModelIDs(f.catalog(t, 8)))

	_, err = f.svc.CreateEntry(ctx, 404, OtohaCatalogEntryInput{ModelID: "gpt-6-luna"})
	require.ErrorIs(t, err, ErrGroupNotFound)
}

func TestOtohaCatalogUpdateKeepsTheEntrysPlace(t *testing.T) {
	f := newOtohaCatalogFixture()
	a := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})
	_, err := f.svc.UpdateEntry(context.Background(), otohaGroupID, a.ID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true, Name: "Luna"})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-luna", "deepseek-v4"}, otohaModelIDs(f.catalog(t, otohaGroupID)))
}

// ---- prefill ----

func TestOtohaCatalogPrefillFillsFromUpstreamMetadataAndPrices(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.groups[otohaGroupID].RateMultiplier = 2
	f.metadata["gpt-6-luna"] = OtohaModelMetadata{
		Name: "GPT-6 Luna", Description: "OpenAI model", Inputs: []string{"text", "image"}, Tools: true,
		Context: 1_050_000, MaxOutput: 128_000, Reasoning: []string{"low", "medium", "high"}, DefaultReasoning: "medium",
	}

	draft, err := f.svc.Prefill(context.Background(), otohaGroupID, " gpt-6-luna ")
	require.NoError(t, err)
	require.Equal(t, "gpt-6-luna", draft.Entry.ModelID)
	require.Equal(t, "GPT-6 Luna", draft.Entry.Name)
	require.Equal(t, "OpenAI model", draft.Entry.Description)
	require.Equal(t, []string{"text", "image"}, draft.Entry.Inputs)
	require.True(t, draft.Entry.Tools)
	require.Equal(t, 1_050_000, draft.Entry.Context)
	require.Equal(t, 128_000, draft.Entry.MaxOutput)
	require.Equal(t, []string{"low", "medium", "high"}, draft.Entry.Reasoning)
	require.Equal(t, "medium", draft.Entry.DefaultReasoning)
	require.True(t, draft.Entry.Enabled)
	require.True(t, draft.MetadataFound)
	require.NotNil(t, draft.UpstreamPrice)
	require.Equal(t, 2.5, draft.UpstreamPrice.Input)
	require.NotNil(t, draft.SalePrice)
	require.Equal(t, 5.0, draft.SalePrice.Input)
	require.Equal(t, 20.0, draft.SalePrice.Output)
	require.Equal(t, OtohaCostStandard, draft.CostTier)
	require.Nil(t, f.catalog(t, otohaGroupID), "a prefill saves nothing")
}

func TestOtohaCatalogPrefillOfAnUnknownModelStillGivesADraft(t *testing.T) {
	f := newOtohaCatalogFixture()
	draft, err := f.svc.Prefill(context.Background(), otohaGroupID, "mystery-1")
	require.NoError(t, err)
	require.Equal(t, "mystery-1", draft.Entry.ModelID)
	require.Equal(t, "mystery-1", draft.Entry.Name)
	require.Equal(t, []string{"text"}, draft.Entry.Inputs)
	require.False(t, draft.MetadataFound)
	require.Nil(t, draft.UpstreamPrice)
	require.Nil(t, draft.SalePrice)

	_, err = f.svc.Prefill(context.Background(), otohaGroupID, "")
	requireOtohaBadRequest(t, err)
	_, err = f.svc.Prefill(context.Background(), 404, "gpt-6-luna")
	require.ErrorIs(t, err, ErrGroupNotFound)
}

// ---- the admin's view ----

func TestOtohaCatalogAdminViewExplainsEachEntryAndPreviewsTheApp(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.groups[otohaGroupID].RateMultiplier = 2
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-luna", "deepseek-v4", "no-price-model", "claude-opus"}}
	f.routing["claude-opus"] = false
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: false})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "grok-5", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "no-price-model", Enabled: true})

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, otohaGroupID, view.GroupID)
	require.Equal(t, "Otoha", view.GroupName)
	require.Equal(t, 2.0, view.RateMultiplier)
	require.Len(t, view.Entries, 5)

	byModel := map[string]OtohaCatalogAdminEntry{}
	for _, e := range view.Entries {
		byModel[e.ModelID] = e
	}
	luna := byModel["gpt-6-luna"]
	require.True(t, luna.InCatalog)
	require.Empty(t, luna.Problem)
	require.Equal(t, 2.5, luna.UpstreamPrice.Input)
	require.Equal(t, 5.0, luna.SalePrice.Input)
	require.Equal(t, OtohaCostStandard, luna.EffectiveCost)

	require.Equal(t, OtohaCatalogProblemDisabled, byModel["deepseek-v4"].Problem)
	require.NotNil(t, byModel["deepseek-v4"].SalePrice, "a disabled entry still shows its price")
	require.Equal(t, OtohaCatalogProblemNotAllowed, byModel["grok-5"].Problem)
	require.Equal(t, OtohaCatalogProblemNoAccount, byModel["claude-opus"].Problem)
	require.Equal(t, OtohaCatalogProblemNoPrice, byModel["no-price-model"].Problem)
	for _, model := range []string{"deepseek-v4", "grok-5", "claude-opus", "no-price-model"} {
		require.False(t, byModel[model].InCatalog, model)
	}

	require.NotNil(t, view.Preview)
	require.Equal(t, f.catalog(t, otohaGroupID), view.Preview, "the preview is exactly what the app receives")
}

func TestOtohaCatalogAdminViewOfAnUnknownGroup(t *testing.T) {
	f := newOtohaCatalogFixture()
	_, err := f.svc.AdminView(context.Background(), 404)
	require.ErrorIs(t, err, ErrGroupNotFound)

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Empty(t, view.Entries)
	require.Nil(t, view.Preview, "no entries, no catalog: the app keeps the plain model list")
}

// ---- this period's usage per model ----

func TestOtohaUsagePeriodFollowsThePlansMonthlyWindow(t *testing.T) {
	start := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	plan := &UserSubscription{
		StartsAt: start, ExpiresAt: start.Add(365 * 24 * time.Hour), MonthlyWindowStart: &start,
	}
	now := start.Add(10 * 24 * time.Hour)
	from, until := OtohaUsagePeriod(plan, now)
	require.Equal(t, start, from)
	require.Equal(t, start.Add(30*24*time.Hour), until)

	later := start.Add(45 * 24 * time.Hour)
	from, until = OtohaUsagePeriod(plan, later)
	require.Equal(t, start.Add(30*24*time.Hour), from, "a window that has run out moves on, as the plan's does")
	require.Equal(t, start.Add(60*24*time.Hour), until)
}

func TestOtohaUsagePeriodWithoutAPlanIsTheCalendarMonth(t *testing.T) {
	now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.Local)
	from, until := OtohaUsagePeriod(nil, now)
	require.Equal(t, 2026, from.Year())
	require.Equal(t, time.October, from.Month())
	require.Equal(t, 1, from.Day())
	require.Equal(t, 0, from.Hour())
	require.Equal(t, time.November, until.Month())
	require.Equal(t, 1, until.Day())

	from, _ = OtohaUsagePeriod(&UserSubscription{}, now)
	require.Equal(t, time.October, from.Month(), "a plan without a monthly window counts as none")
}

func TestOtohaModelUsageKeepsOnlyWhatTheUserPays(t *testing.T) {
	usage := OtohaModelUsageFromStats([]usagestats.ModelStat{
		{Model: "gpt-6-luna", Requests: 3, InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 5, CacheReadTokens: 20, TotalTokens: 175, Cost: 1, ActualCost: 1.5, AccountCost: 0.4},
	})
	require.Equal(t, []OtohaModelUsage{{
		Model: "gpt-6-luna", Requests: 3, InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 5,
		CacheReadTokens: 20, TotalTokens: 175, Cost: 1.5,
	}}, usage)

	body, err := json.Marshal(usage)
	require.NoError(t, err)
	require.NotContains(t, string(body), "account_cost")
	require.NotContains(t, string(body), "actual_cost")
	require.Equal(t, []OtohaModelUsage{}, OtohaModelUsageFromStats(nil))
}

func TestOtohaCatalogPrefillDropsUpstreamValuesTheCatalogCannotTake(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.metadata["odd-model"] = OtohaModelMetadata{
		Name: "Odd", Inputs: []string{"text", "smell", "image"}, Context: -5,
		Reasoning: []string{"low", "Very High"}, DefaultReasoning: "none",
	}
	draft, err := f.svc.Prefill(context.Background(), otohaGroupID, "odd-model")
	require.NoError(t, err)
	require.True(t, draft.MetadataFound)
	require.Equal(t, "Odd", draft.Entry.Name)
	require.Equal(t, []string{"text", "image"}, draft.Entry.Inputs)
	require.Zero(t, draft.Entry.Context)
	require.Equal(t, []string{"low"}, draft.Entry.Reasoning)
	require.Empty(t, draft.Entry.DefaultReasoning)
}

// ---- priced for the key's owner ----

func TestOtohaCatalogAtAUsersOwnRate(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.groups[otohaGroupID].RateMultiplier = 1.5
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	ctx := context.Background()

	group := f.catalog(t, otohaGroupID)
	own, err := f.svc.CatalogForGroupAtRate(ctx, otohaGroupID, 0.2)
	require.NoError(t, err)
	require.Equal(t, 0.5, own.Models[0].Price.Input, "upstream 2.5 at the user's 0.2")
	require.Equal(t, 2.0, own.Models[0].Price.Output)
	require.Equal(t, 0.05, *own.Models[0].Price.CachedInput)
	require.Equal(t, OtohaCostLow, own.Models[0].Cost, "the tier follows what this user pays")
	require.NotEqual(t, group.Revision, own.Revision, "a different price is a different catalog")
	require.Equal(t, 3.75, group.Models[0].Price.Input, "the group's own catalog is unchanged")

	same, err := f.svc.CatalogForGroupAtRate(ctx, otohaGroupID, 1.5)
	require.NoError(t, err)
	require.Equal(t, group, same)

	none, err := f.svc.CatalogForGroupAtRate(ctx, 8, 0.5)
	require.NoError(t, err)
	require.Nil(t, none, "a group without entries still has no catalog")
}

func TestOtohaUsagePeriodEndsWithThePlan(t *testing.T) {
	start := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	plan := &UserSubscription{StartsAt: start, ExpiresAt: start.Add(45 * 24 * time.Hour), MonthlyWindowStart: &start}
	_, until := OtohaUsagePeriod(plan, start.Add(35*24*time.Hour))
	require.Equal(t, plan.ExpiresAt, until, "the last, shorter period ends when the plan does")
}
