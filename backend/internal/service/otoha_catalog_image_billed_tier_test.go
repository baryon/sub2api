//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TASK-77: the catalog prices each size at the tier billing really charges, which for some upstreams is the tier of
// the size the upstream drew, not the size asked for.

// observingImageRepo is the image repository of the fixture plus what the group's usage log says was billed.
type observingImageRepo struct {
	*fakeOtohaImageRepo
	mu       sync.Mutex
	rows     []OtohaImageObservation
	err      error
	calls    int
	lastFrom time.Time
	lastGrp  int64
}

func (r *observingImageRepo) ObservedImageOutputs(_ context.Context, groupID int64, since time.Time) ([]OtohaImageObservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastFrom, r.lastGrp = since, groupID
	if r.err != nil {
		return nil, r.err
	}
	return append([]OtohaImageObservation{}, r.rows...), nil
}

func newObservedImageFixture(rows ...OtohaImageObservation) (*otohaImageFixture, *observingImageRepo) {
	f := newOtohaImageFixture()
	repo := &observingImageRepo{fakeOtohaImageRepo: f.images, rows: rows}
	f.svc.WithImages(repo, f.pricer, f.routing)
	return f, repo
}

func observed(model, requested, tier, output string) OtohaImageObservation {
	return OtohaImageObservation{Model: model, RequestedSize: requested, Tier: tier, OutputSize: output}
}

func sizeOf(t *testing.T, catalog *OtohaCatalog, model, size string) OtohaImageSize {
	t.Helper()
	for _, m := range catalog.ImageModels {
		if m.ID != model {
			continue
		}
		for _, s := range m.Sizes {
			if s.Size == size {
				return s
			}
		}
	}
	t.Fatalf("%s %s is not in the catalog", model, size)
	return OtohaImageSize{}
}

func TestOtohaImageSizeIsPricedAtTheTierBillingChargedForItsOutput(t *testing.T) {
	f, _ := newObservedImageFixture(observed("gpt-image-2", "1024x1024", "2K", "1254x1254"))
	f.addImage(t, gptImageInput())

	catalog := f.catalog(t, otohaGroupID)
	asked := sizeOf(t, catalog, "gpt-image-2", "1024x1024")
	require.Equal(t, "2K", asked.Tier, "1024x1024 comes back as 1254x1254, which billing charges as 2K")
	require.InDelta(t, 0.06, asked.Price, 1e-9)

	other := sizeOf(t, catalog, "gpt-image-2", "1536x1024")
	require.Equal(t, "2K", other.Tier)
	require.InDelta(t, 0.06, other.Price, 1e-9)
	huge := sizeOf(t, catalog, "gpt-image-2", "3840x2160")
	require.Equal(t, "4K", huge.Tier, "a size with no record is classified by its own size")
	require.InDelta(t, 0.12, huge.Price, 1e-9)
}

func TestOtohaImageSizeWithoutRecordsIsPricedByItsOwnSize(t *testing.T) {
	f, _ := newObservedImageFixture()
	f.addImage(t, gptImageInput())

	small := sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024")
	require.Equal(t, "1K", small.Tier)
	require.InDelta(t, 0.04, small.Price, 1e-9)
}

func TestOtohaImageObservationsOfOtherModelsOrSizesDoNotCount(t *testing.T) {
	f, _ := newObservedImageFixture(
		observed("grok-imagine-image", "1024x1024", "4K", "3840x2160"),
		observed("gpt-image-2", "512x512", "2K", "1254x1254"),
	)
	f.addImage(t, gptImageInput())

	require.Equal(t, "1K", sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024").Tier)
}

func TestOtohaImageSizeUsesTheHighestObservedTier(t *testing.T) {
	f, _ := newObservedImageFixture(
		observed("gpt-image-2", "1024x1024", "1K", "1024x1024"),
		observed("gpt-image-2", "1024x1024", "4K", "3840x2160"),
		observed("gpt-image-2", "1024x1024", "2K", "1254x1254"),
	)
	f.addImage(t, gptImageInput())

	size := sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024")
	require.Equal(t, "4K", size.Tier)
	require.InDelta(t, 0.12, size.Price, 1e-9)
}

func TestOtohaImageSizeUsesAnObservedTierBelowTheRequestedOne(t *testing.T) {
	f, _ := newObservedImageFixture(observed("gpt-image-2", "3840x2160", "2K", "1536x1024"))
	f.addImage(t, gptImageInput())

	size := sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "3840x2160")
	require.Equal(t, "2K", size.Tier, "what was charged is what is shown, even below the size asked for")
	require.InDelta(t, 0.06, size.Price, 1e-9)
}

func TestOtohaImageObservedSizesMatchWithoutCaseOrSpaces(t *testing.T) {
	f, _ := newObservedImageFixture(observed(" gpt-image-2 ", " 1024X1024 ", "2K", "1254x1254"))
	f.addImage(t, gptImageInput())

	require.Equal(t, "2K", sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024").Tier)
}

func TestOtohaImageObservedTierWithoutAPriceKeepsTheModelOutOfTheCatalog(t *testing.T) {
	f, _ := newObservedImageFixture(observed("gpt-image-2", "1024x1024", "4K", "3840x2160"))
	delete(f.pricer["gpt-image-2"], "4K")
	f.addImage(t, gptImageInput())

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaCatalogProblemNoPrice, view.ImageEntries[0].Problem)
	require.Empty(t, f.catalog(t, otohaGroupID).ImageModels)
}

func TestOtohaImageAdminViewSaysWhyEachSizeIsBilledAsItIs(t *testing.T) {
	f, _ := newObservedImageFixture(observed("gpt-image-2", "1024x1024", "2K", "1254x1254"))
	f.addImage(t, gptImageInput())

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, []OtohaImageSizeBasis{
		{Size: "1024x1024", RequestedTier: "1K", BilledTier: "2K", ObservedOutputSize: "1254x1254", Basis: OtohaImageBasisObserved},
		{Size: "1536x1024", RequestedTier: "2K", BilledTier: "2K", Basis: OtohaImageBasisRequested},
		{Size: "3840x2160", RequestedTier: "4K", BilledTier: "4K", Basis: OtohaImageBasisRequested},
	}, view.ImageEntries[0].SizeBasis)
	require.Equal(t, "2K", view.Preview.ImageModels[0].Sizes[0].Tier, "the preview is what the app receives")
}

func TestOtohaImageObservationsAreReadForTheGroupOverThirtyDaysAndCached(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f, repo := newObservedImageFixture(observed("gpt-image-2", "1024x1024", "2K", "1254x1254"))
	f.svc.now = func() time.Time { return now }
	f.addImage(t, gptImageInput())

	f.catalog(t, otohaGroupID)
	_, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	f.svc.InvalidateGroup(otohaGroupID)
	f.catalog(t, otohaGroupID)
	require.Equal(t, 1, repo.calls, "the usage log is not read for every catalog read")
	require.Equal(t, int64(otohaGroupID), repo.lastGrp)
	require.True(t, repo.lastFrom.Equal(now.AddDate(0, 0, -30)), "last 30 days, got %s", repo.lastFrom)

	now = now.Add(otohaImageObservationTTL + time.Second)
	repo.mu.Lock()
	repo.rows = []OtohaImageObservation{observed("gpt-image-2", "1024x1024", "4K", "3840x2160")}
	repo.mu.Unlock()
	require.Equal(t, "4K", sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024").Tier)
	require.Equal(t, 2, repo.calls)
}

func TestOtohaImageCatalogFallsBackToRequestedSizesWhenTheUsageLogCannotBeRead(t *testing.T) {
	f, repo := newObservedImageFixture()
	repo.err = errors.New("db down")
	f.addImage(t, gptImageInput())

	require.Equal(t, "1K", sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024").Tier)

	// A failed read is not remembered: the next read tries again and shows the real tier.
	repo.err = nil
	repo.rows = []OtohaImageObservation{observed("gpt-image-2", "1024x1024", "2K", "1254x1254")}
	require.Equal(t, "2K", sizeOf(t, f.catalog(t, otohaGroupID), "gpt-image-2", "1024x1024").Tier)
}

func TestOtohaImageCatalogJSONShapeIsUnchangedByObservations(t *testing.T) {
	f, _ := newObservedImageFixture(observed("gpt-image-2", "1024x1024", "2K", "1254x1254"))
	f.addImage(t, gptImageInput())

	body, err := json.Marshal(f.catalog(t, otohaGroupID).ImageModels[0].Sizes[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"size":"1024x1024","tier":"2K","price":0.06}`, string(body))
}

func TestOtohaImageSizeWithoutObservationSourceStaysByRequestedSize(t *testing.T) {
	f := newOtohaImageFixture() // the repository cannot report what was billed
	f.addImage(t, gptImageInput())

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaImageBasisRequested, view.ImageEntries[0].SizeBasis[0].Basis)
	require.Equal(t, "1K", view.ImageEntries[0].SizeBasis[0].BilledTier)
}

func TestInferOtohaImageBilledTier(t *testing.T) {
	rows := []OtohaImageObservation{
		observed("m", "1024x1024", "2K", "1254x1254"),
		observed("m", "1024x1024", "4K", "3840x2160"),
		observed("m", "1024x1024", "4K", "2160x3840"),
	}
	got := inferOtohaImageBilledTier("m", "1024x1024", indexOtohaImageObservations(rows))
	require.Equal(t, OtohaImageSizeBasis{Size: "1024x1024", RequestedTier: "1K", BilledTier: "4K", ObservedOutputSize: "3840x2160", Basis: OtohaImageBasisObserved}, got,
		"the first of the highest tier wins, so the most recent record when rows come newest first")

	got = inferOtohaImageBilledTier("m", "1024x1024", indexOtohaImageObservations([]OtohaImageObservation{observed("m", "1024x1024", "mixed", "")}))
	require.Equal(t, OtohaImageBasisRequested, got.Basis, "a tier billing does not price per image is not a basis")
}
