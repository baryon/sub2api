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

// TASK-81: video, voice and quick-judgement (Jev) models next to the chat and image models. They are found from the
// group's accounts, priced as billing prices them times the rate, and listed in their own arrays.

// ---- fakes ----

type fakeOtohaMediaStore struct {
	mu      sync.Mutex
	entries map[int64][]OtohaMediaEntry
	saves   int
	loadErr error
}

func newFakeOtohaMediaStore() *fakeOtohaMediaStore {
	return &fakeOtohaMediaStore{entries: map[int64][]OtohaMediaEntry{}}
}

func (s *fakeOtohaMediaStore) LoadMediaEntries(_ context.Context, groupID int64) ([]OtohaMediaEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return append([]OtohaMediaEntry{}, s.entries[groupID]...), nil
}

func (s *fakeOtohaMediaStore) SaveMediaEntries(_ context.Context, groupID int64, entries []OtohaMediaEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	s.entries[groupID] = append([]OtohaMediaEntry{}, entries...)
	return nil
}

// fakeOtohaMediaPricer gives the price per second of a video model by resolution and the price of a voice unit by
// mode, before any rate.
type fakeOtohaMediaPricer struct {
	video map[string]map[string]float64
	voice map[string]float64
}

func (p fakeOtohaMediaPricer) VideoSecondPrice(_ context.Context, _ *Group, modelID, resolution string) (float64, bool) {
	price, ok := p.video[modelID][resolution]
	return price, ok
}

func (p fakeOtohaMediaPricer) VoiceUnitPrice(_ context.Context, _ *Group, mode string) (float64, bool) {
	price, ok := p.voice[mode]
	return price, ok
}

// fakeOtohaMediaRouting maps "kind/model" to the problem the route has.
type fakeOtohaMediaRouting map[string]string

func (r fakeOtohaMediaRouting) MediaRoute(_ context.Context, _ *Group, kind, modelID string) OtohaModelRoute {
	if problem, ok := r[kind+"/"+modelID]; ok {
		return OtohaModelRoute{Problem: problem}
	}
	return OtohaModelRoute{}
}

type fakeOtohaMediaAccounts struct {
	mu       sync.Mutex
	listings []OtohaAccountListing
	err      error
	calls    int
}

func (a *fakeOtohaMediaAccounts) ListGroupMediaAccounts(context.Context, int64) ([]OtohaAccountListing, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.listings, a.err
}

func (a *fakeOtohaMediaAccounts) set(listings ...OtohaAccountListing) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listings = listings
}

type fakeOtohaMediaReports struct {
	mu      sync.Mutex
	reports map[int64]OtohaMediaDiscovery
}

func (s *fakeOtohaMediaReports) LoadMediaDiscovery(_ context.Context, groupID int64) (*OtohaMediaDiscovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report, ok := s.reports[groupID]
	if !ok {
		return nil, nil
	}
	return &report, nil
}

func (s *fakeOtohaMediaReports) SaveMediaDiscovery(_ context.Context, groupID int64, report OtohaMediaDiscovery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reports == nil {
		s.reports = map[int64]OtohaMediaDiscovery{}
	}
	s.reports[groupID] = report
	return nil
}

func typeSafeAccount(id int64, name string) *Account {
	return &Account{ID: id, Name: name, Platform: PlatformTypeSafe, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "ts-test"}}
}

type otohaMediaFixture struct {
	*otohaCatalogFixture
	store    *fakeOtohaMediaStore
	pricer   fakeOtohaMediaPricer
	routing  fakeOtohaMediaRouting
	accounts *fakeOtohaMediaAccounts
	reports  *fakeOtohaMediaReports
}

var (
	grokOAuthListing = OtohaAccountListing{
		Account: grokAccount(7, "Grok OAuth", AccountTypeOAuth),
		Models:  listed("grok-4.3", "grok-imagine-image", "grok-imagine-video", "grok-imagine-video-1.5"),
	}
	jevListing = OtohaAccountListing{Account: typeSafeAccount(11, "Jev lilong")}
)

// newOtohaMediaFixture is the Otoha group of the deployed instance: a composite group at rate 1.5 with a Grok
// account and a TypeSafe one, and billing prices like billing's defaults.
func newOtohaMediaFixture(listings ...OtohaAccountListing) *otohaMediaFixture {
	f := &otohaMediaFixture{
		otohaCatalogFixture: newOtohaCatalogFixture(),
		store:               newFakeOtohaMediaStore(),
		pricer: fakeOtohaMediaPricer{
			video: map[string]map[string]float64{
				"grok-imagine-video":     {"480p": 0.05, "720p": 0.07},
				"grok-imagine-video-1.5": {"480p": 0.08, "720p": 0.14, "1080p": 0.25},
			},
			voice: map[string]float64{"tts": 15, "stt": 0.10},
		},
		routing:  fakeOtohaMediaRouting{},
		accounts: &fakeOtohaMediaAccounts{listings: listings},
		reports:  &fakeOtohaMediaReports{},
	}
	f.groups[otohaGroupID].Platform = PlatformComposite
	f.groups[otohaGroupID].RateMultiplier = 1.5
	f.otohaCatalogFixture.pricer["jev-latest"] = OtohaModelPrice{Input: 0.042}
	f.svc.WithMedia(f.store, f.pricer, f.routing).WithMediaDiscovery(f.accounts, f.reports)
	f.svc.now = func() time.Time { return discoveryNow }
	return f
}

func (f *otohaMediaFixture) discover(t *testing.T) *OtohaMediaDiscovery {
	t.Helper()
	report, err := f.svc.DiscoverMedia(context.Background(), otohaGroupID, OtohaImageTriggerAdmin)
	require.NoError(t, err)
	return report
}

func (f *otohaMediaFixture) stored(t *testing.T) map[string]OtohaMediaEntry {
	t.Helper()
	entries, err := f.store.LoadMediaEntries(context.Background(), otohaGroupID)
	require.NoError(t, err)
	out := map[string]OtohaMediaEntry{}
	for _, e := range entries {
		out[e.Kind+"/"+e.ModelID] = e
	}
	return out
}

func (f *otohaMediaFixture) fresh(t *testing.T) *OtohaCatalog {
	t.Helper()
	f.svc.InvalidateGroup(otohaGroupID)
	return f.catalog(t, otohaGroupID)
}

func resultsByKey(report *OtohaMediaDiscovery) map[string]OtohaMediaDiscoveryCandidate {
	out := map[string]OtohaMediaDiscoveryCandidate{}
	for _, c := range report.Candidates {
		out[c.Kind+"/"+c.ModelID] = c
	}
	return out
}

// ---- what a list says about video ----

func TestOtohaListedModelsDeclareVideoByOutputModalitiesOrType(t *testing.T) {
	body := []byte(`{"models":[
		{"id":"clip-a","output_modalities":["video"]},
		{"id":"clip-b","type":"video"},
		{"id":"drawer","output_modalities":["image"]},
		{"id":"grok-4.3"}]}`)

	got := byListedID(parseOtohaListedModels(body, false))

	require.True(t, got["clip-a"].Video)
	require.Equal(t, "output_modalities", got["clip-a"].VideoDeclaredBy)
	require.True(t, got["clip-b"].Video)
	require.Equal(t, "type", got["clip-b"].VideoDeclaredBy)
	require.False(t, got["drawer"].Video)
	require.False(t, got["grok-4.3"].Video)
	require.Equal(t, OtohaListedKindOther, got["clip-b"].Kind, "a video model is still not an image model")
}

// ---- candidates from accounts ----

func mediaCandidates(listings ...OtohaAccountListing) map[string]otohaMediaCandidate {
	out := map[string]otohaMediaCandidate{}
	for _, c := range otohaMediaCandidatesFrom(listings) {
		out[c.Kind+"/"+c.ModelID] = c
	}
	return out
}

func TestOtohaMediaCandidatesFromAGrokListWithoutKindUseTheVideoFamily(t *testing.T) {
	got := mediaCandidates(grokOAuthListing)

	require.Contains(t, got, "video/grok-imagine-video")
	require.Contains(t, got, "video/grok-imagine-video-1.5")
	require.Equal(t, OtohaImageEvidenceKnownFamily, got["video/grok-imagine-video"].Evidence)
	require.Equal(t, 1, got["video/grok-imagine-video"].Served)
	require.NotContains(t, got, "video/grok-imagine-image", "an image model is not a video model")
	require.NotContains(t, got, "video/grok-4.3")
}

func TestOtohaMediaCandidatesTakeAVideoModelTheListDeclaresWhateverItsName(t *testing.T) {
	listing := OtohaAccountListing{
		Account: grokAccount(7, "grok", AccountTypeAPIKey),
		Models:  []OtohaListedModel{{ID: "grok-clip-9", Name: "Clip 9", Video: true, VideoDeclaredBy: "output_modalities"}},
	}

	got := mediaCandidates(listing)

	require.Contains(t, got, "video/grok-clip-9")
	require.Equal(t, OtohaImageEvidenceDeclared, got["video/grok-clip-9"].Evidence)
	require.Equal(t, "output_modalities", got["video/grok-clip-9"].DeclaredBy)
	require.Equal(t, "Clip 9", got["video/grok-clip-9"].Name)
}

func TestOtohaMediaCandidatesFallBackToTheGatewaysTableWhenTheGrokListFails(t *testing.T) {
	got := mediaCandidates(OtohaAccountListing{Account: grokAccount(7, "grok", AccountTypeOAuth), Err: OtohaImageListFailed})

	require.Contains(t, got, "video/grok-imagine-video")
	require.Contains(t, got, "video/grok-imagine-video-1.5")
	require.Equal(t, OtohaImageEvidenceBuiltinCatalog, got["video/grok-imagine-video"].Evidence)
	require.Equal(t, "Grok Imagine Video", got["video/grok-imagine-video"].Name)
}

func TestOtohaMediaCandidatesGrokAccountsOfferBothVoiceFunctions(t *testing.T) {
	got := mediaCandidates(grokOAuthListing)

	require.Contains(t, got, "voice/tts")
	require.Contains(t, got, "voice/stt")
	require.Equal(t, OtohaImageEvidenceBuiltinCatalog, got["voice/tts"].Evidence, "xAI lists no voice models; the gateway's own endpoints are the evidence")
	require.Equal(t, 1, got["voice/tts"].Served)
}

func TestOtohaMediaCandidatesATypeSafeAccountOffersJev(t *testing.T) {
	got := mediaCandidates(jevListing)

	require.Len(t, got, 1)
	require.Contains(t, got, "quick/jev-latest")
	require.Equal(t, OtohaImageEvidenceBuiltinCatalog, got["quick/jev-latest"].Evidence)
	require.Equal(t, "Jev", got["quick/jev-latest"].Name)
	require.Equal(t, []string{"Jev lilong"}, got["quick/jev-latest"].Accounts)
}

func TestOtohaMediaCandidatesSkipVideoModelsTheAccountMappingDoesNotServe(t *testing.T) {
	account := grokAccount(7, "grok", AccountTypeAPIKey)
	account.Credentials["model_mapping"] = map[string]any{"grok-imagine-video-1.5": "grok-imagine-video-1.5"}

	got := mediaCandidates(OtohaAccountListing{Account: account, Models: listed("grok-imagine-video", "grok-imagine-video-1.5")})

	require.Equal(t, 0, got["video/grok-imagine-video"].Served, "listed upstream, but the account's mapping does not let it through")
	require.Equal(t, 1, got["video/grok-imagine-video-1.5"].Served)
}

func TestOtohaMediaCandidatesIgnoreAccountsOfOtherProviders(t *testing.T) {
	got := mediaCandidates(
		OtohaAccountListing{Account: openAIKeyAccount(1, "gpt", nil), Models: listed("gpt-5.4", "gpt-image-2")},
		OtohaAccountListing{Account: &Account{ID: 3, Name: "claude", Platform: PlatformAnthropic, Type: AccountTypeAPIKey}},
	)
	require.Empty(t, got)
}

func TestOtohaMediaCandidatesMergeTheSameModelFromSeveralAccounts(t *testing.T) {
	got := mediaCandidates(
		OtohaAccountListing{Account: grokAccount(7, "grok a", AccountTypeAPIKey), Models: listed("grok-imagine-video")},
		OtohaAccountListing{Account: grokAccount(8, "grok b", AccountTypeOAuth), Models: listed("grok-imagine-video")},
	)

	require.Equal(t, []string{"grok a", "grok b"}, got["video/grok-imagine-video"].Accounts)
	require.Equal(t, 2, got["video/grok-imagine-video"].Served)
	require.Equal(t, 2, got["voice/tts"].Served)
}

// ---- the plan ----

func mediaCandidate(kind, id, name string, served int) otohaMediaCandidate {
	return otohaMediaCandidate{Kind: kind, ModelID: id, Name: name, Accounts: []string{"acc"}, Served: served, Evidence: OtohaImageEvidenceBuiltinCatalog}
}

func planMedia(candidates []otohaMediaCandidate, existing []OtohaMediaEntry, allListed bool, reason func(otohaMediaCandidate) string) otohaMediaPlan {
	return planOtohaMediaDiscovery(otohaMediaPlanInput{Candidates: candidates, Existing: existing, AllListed: allListed, Reason: reason, Now: discoveryNow})
}

func noMediaReason(otohaMediaCandidate) string { return "" }

func TestOtohaMediaPlanCreatesEnabledEntriesWithTheNameTheUpstreamGave(t *testing.T) {
	p := planMedia([]otohaMediaCandidate{
		mediaCandidate(OtohaMediaKindVideo, "grok-imagine-video", "Grok Imagine Video", 1),
		mediaCandidate(OtohaMediaKindQuick, "jev-latest", "", 1),
	}, nil, true, noMediaReason)

	require.Len(t, p.Entries, 2)
	require.Equal(t, OtohaMediaKindVideo, p.Entries[0].Kind)
	require.Equal(t, "Grok Imagine Video", p.Entries[0].Name)
	require.True(t, p.Entries[0].Enabled)
	require.Equal(t, "jev-latest", p.Entries[1].Name, "no name from the upstream: the model ID")
	require.Less(t, p.Entries[0].SortOrder, p.Entries[1].SortOrder)
	require.Empty(t, p.Entries[0].ManualFields)
	require.Equal(t, OtohaImageResultAdded, p.Candidates[0].Result)
}

func TestOtohaMediaPlanKeepsEveryValueTheAdminChanged(t *testing.T) {
	existing := []OtohaMediaEntry{{
		Kind: OtohaMediaKindVideo, ModelID: "grok-imagine-video", Name: "Clips", Description: "Short clips", Enabled: false, SortOrder: 10,
		ManualFields: []string{"name", "description", "enabled"},
	}}

	p := planMedia([]otohaMediaCandidate{mediaCandidate(OtohaMediaKindVideo, "grok-imagine-video", "Grok Imagine Video", 1)}, existing, true, noMediaReason)

	got := p.Entries[0]
	require.Equal(t, "Clips", got.Name)
	require.Equal(t, "Short clips", got.Description)
	require.False(t, got.Enabled, "the admin's switch is never refreshed")
	require.Equal(t, OtohaImageResultUnchanged, p.Candidates[0].Result)
}

func TestOtohaMediaPlanRefreshesTheNameTheAdminDidNotChange(t *testing.T) {
	existing := []OtohaMediaEntry{{Kind: OtohaMediaKindVideo, ModelID: "grok-imagine-video", Name: "old name", Enabled: true, SortOrder: 10, ManualFields: []string{}}}

	p := planMedia([]otohaMediaCandidate{mediaCandidate(OtohaMediaKindVideo, "grok-imagine-video", "Grok Imagine Video", 1)}, existing, true, noMediaReason)

	require.Equal(t, "Grok Imagine Video", p.Entries[0].Name)
	require.Equal(t, OtohaImageResultUpdated, p.Candidates[0].Result)
}

func TestOtohaMediaPlanMarksAnEntryUnavailableOnlyWhenEveryAccountWasRead(t *testing.T) {
	existing := []OtohaMediaEntry{{Kind: OtohaMediaKindQuick, ModelID: "jev-latest", Name: "Jev", Enabled: true, SortOrder: 10}}

	gone := planMedia(nil, existing, true, noMediaReason)
	require.NotNil(t, gone.Entries[0].UnavailableSince)
	require.True(t, discoveryNow.Equal(*gone.Entries[0].UnavailableSince))
	require.Equal(t, []string{"quick/jev-latest"}, gone.Missing)

	unknown := planMedia(nil, existing, false, noMediaReason)
	require.Nil(t, unknown.Entries[0].UnavailableSince, "an account that could not be read does not make a model disappear")
	require.Empty(t, unknown.Missing)
}

func TestOtohaMediaPlanKeepsTheFirstMissingTimeAndRestoresWhenItIsBack(t *testing.T) {
	first := discoveryNow.Add(-48 * time.Hour)
	existing := []OtohaMediaEntry{{Kind: OtohaMediaKindQuick, ModelID: "jev-latest", Name: "Jev", Enabled: true, SortOrder: 10, UnavailableSince: &first}}

	still := planMedia(nil, existing, true, noMediaReason)
	require.True(t, first.Equal(*still.Entries[0].UnavailableSince), "the first time it was missing stays")

	back := planMedia([]otohaMediaCandidate{mediaCandidate(OtohaMediaKindQuick, "jev-latest", "", 1)}, existing, true, noMediaReason)
	require.Nil(t, back.Entries[0].UnavailableSince)
	require.Equal(t, OtohaImageResultRestored, back.Candidates[0].Result)
}

func TestOtohaMediaPlanReportsWhyACandidateWasNotAdded(t *testing.T) {
	reason := func(c otohaMediaCandidate) string {
		if c.ModelID == "tts" {
			return OtohaCatalogProblemNoRoute
		}
		return ""
	}

	p := planMedia([]otohaMediaCandidate{
		mediaCandidate(OtohaMediaKindVoice, "tts", "Grok text to speech", 1),
		mediaCandidate(OtohaMediaKindVoice, "stt", "Grok speech to text", 0),
	}, nil, true, reason)

	require.Empty(t, p.Entries, "nothing is added: one has no route, the other no account serving it")
	require.Equal(t, OtohaImageResultNotAdded, p.Candidates[0].Result)
	require.Equal(t, OtohaCatalogProblemNoRoute, p.Candidates[0].Reason)
	require.Equal(t, OtohaCatalogProblemNoAccount, p.Candidates[1].Reason)
}

func TestOtohaMediaPlanTellsApartKindsOfTheSameID(t *testing.T) {
	existing := []OtohaMediaEntry{{Kind: OtohaMediaKindVideo, ModelID: "x", Name: "video x", Enabled: true, SortOrder: 10}}

	p := planMedia([]otohaMediaCandidate{mediaCandidate(OtohaMediaKindQuick, "x", "quick x", 1)}, existing, true, noMediaReason)

	require.Len(t, p.Entries, 2)
}

// ---- discovery through the service ----

func TestOtohaDiscoverMediaMakesEntriesFromTheGroupsAccounts(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.routing["voice/tts"] = OtohaCatalogProblemNoRoute
	f.routing["voice/stt"] = OtohaCatalogProblemNoRoute

	report := f.discover(t)

	require.Equal(t, discoveryNow, *report.RanAt)
	require.Equal(t, OtohaImageTriggerAdmin, report.Trigger)
	stored := f.stored(t)
	require.Contains(t, stored, "video/grok-imagine-video")
	require.Contains(t, stored, "video/grok-imagine-video-1.5")
	require.Contains(t, stored, "quick/jev-latest")
	require.NotContains(t, stored, "voice/tts", "a composite group cannot serve the voice endpoints, so the entry is not made")

	results := resultsByKey(report)
	require.Equal(t, OtohaImageResultAdded, results["video/grok-imagine-video"].Result)
	require.Equal(t, OtohaImageResultNotAdded, results["voice/tts"].Result)
	require.Equal(t, OtohaCatalogProblemNoRoute, results["voice/tts"].Reason)
	require.Len(t, report.Accounts, 2)
	require.Equal(t, "Grok OAuth", report.Accounts[0].Name)
	require.Equal(t, 4, report.Accounts[0].MediaModels, "two video and two voice candidates on the Grok account")
	require.Equal(t, 1, report.Accounts[1].MediaModels)
}

func TestOtohaDiscoverMediaDoesNotAddWhatTheGroupsModelListBlocks(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-luna", "grok-imagine-video"}}
	f.routing["voice/tts"] = OtohaCatalogProblemNoRoute
	f.routing["voice/stt"] = OtohaCatalogProblemNoRoute

	report := f.discover(t)

	results := resultsByKey(report)
	require.Equal(t, OtohaImageResultAdded, results["video/grok-imagine-video"].Result)
	require.Equal(t, OtohaImageResultNotAdded, results["video/grok-imagine-video-1.5"].Result)
	require.Equal(t, OtohaImageReasonBlockedByGroupModels, results["video/grok-imagine-video-1.5"].Reason)
	require.Equal(t, OtohaImageReasonBlockedByGroupModels, results["quick/jev-latest"].Reason)
	require.NotContains(t, f.stored(t), "quick/jev-latest")
}

func TestOtohaDiscoverMediaVoiceIsNotHeldBackByTheModelList(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-luna"}}

	report := f.discover(t)

	results := resultsByKey(report)
	require.Equal(t, OtohaImageResultAdded, results["voice/tts"].Result, "a voice request carries no model name, so the list does not apply to it")
	require.Equal(t, OtohaImageResultAdded, results["voice/stt"].Result)
}

func TestOtohaDiscoverMediaOnlyAddsModelsTheGatewayServes(t *testing.T) {
	f := newOtohaMediaFixture(OtohaAccountListing{
		Account: grokAccount(7, "grok", AccountTypeAPIKey),
		Models:  []OtohaListedModel{{ID: "some-clip-model", Video: true, VideoDeclaredBy: "type"}},
	})

	report := f.discover(t)

	require.Equal(t, OtohaImageReasonGatewayNotSupported, resultsByKey(report)["video/some-clip-model"].Reason,
		"billing and the video endpoint know the grok-imagine-video family only")
}

func TestOtohaDiscoverMediaAnAdminEditSticksThroughTheNextCheck(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.discover(t)

	_, err := f.svc.UpdateMediaEntry(context.Background(), otohaGroupID, OtohaMediaEntryInput{
		Kind: OtohaMediaKindQuick, ModelID: "jev-latest", Name: "Jev (fast judgement)", Description: "Answers yes or no questions.", Enabled: false,
	})
	require.NoError(t, err)
	f.discover(t)

	got := f.stored(t)["quick/jev-latest"]
	require.Equal(t, "Jev (fast judgement)", got.Name)
	require.Equal(t, "Answers yes or no questions.", got.Description)
	require.False(t, got.Enabled)
	require.ElementsMatch(t, []string{"name", "description", "enabled"}, got.ManualFields)
	require.Empty(t, f.fresh(t).QuickModels, "turned off by the admin")
}

func TestOtohaUpdateMediaEntryChangesOnlyWhatDiffers(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.discover(t)

	updated, err := f.svc.UpdateMediaEntry(context.Background(), otohaGroupID, OtohaMediaEntryInput{
		Kind: OtohaMediaKindVideo, ModelID: "grok-imagine-video", Name: "Grok Imagine Video", Description: "Clips", Enabled: true,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"description"}, updated.ManualFields, "the name and the switch are what discovery had")
	require.Equal(t, "Clips", f.fresh(t).VideoModels[0].Description)
}

func TestOtohaUpdateMediaEntryRejectsUnknownEntriesAndLongText(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.discover(t)

	_, err := f.svc.UpdateMediaEntry(context.Background(), otohaGroupID, OtohaMediaEntryInput{Kind: OtohaMediaKindVideo, ModelID: "nope", Enabled: true})
	require.ErrorIs(t, err, ErrOtohaCatalogEntryNotFound)

	long := make([]byte, otohaDescriptionMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	_, err = f.svc.UpdateMediaEntry(context.Background(), otohaGroupID, OtohaMediaEntryInput{
		Kind: OtohaMediaKindVideo, ModelID: "grok-imagine-video", Description: string(long), Enabled: true,
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrOtohaCatalogEntryNotFound)
}

func TestOtohaDiscoverMediaMarksVanishedModelsUnavailableAndRestoresThem(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.discover(t)
	require.Len(t, f.fresh(t).QuickModels, 1)

	f.accounts.set(grokOAuthListing)
	report := f.discover(t)
	require.Equal(t, []string{"quick/jev-latest"}, report.Missing)
	require.NotNil(t, f.stored(t)["quick/jev-latest"].UnavailableSince)
	require.Empty(t, f.fresh(t).QuickModels, "the account is gone, so the model is not offered")
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	var problem string
	for _, e := range view.MediaEntries {
		if e.ModelID == "jev-latest" {
			problem = e.Problem
		}
	}
	require.Equal(t, OtohaCatalogProblemUpstreamMissing, problem)

	f.accounts.set(grokOAuthListing, jevListing)
	report = f.discover(t)
	require.Equal(t, OtohaImageResultRestored, resultsByKey(report)["quick/jev-latest"].Result)
	require.Len(t, f.fresh(t).QuickModels, 1)
}

func TestOtohaDiscoverMediaFailedListingChangesNothing(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.discover(t)
	savesBefore := f.store.saves

	f.accounts.set(OtohaAccountListing{Account: grokOAuthListing.Account, Err: OtohaImageListFailed}, jevListing)
	report := f.discover(t)

	require.Empty(t, report.Missing)
	require.Equal(t, OtohaImageListFailed, report.Accounts[0].Error)
	require.Len(t, f.stored(t), 5, "the video models stay (the gateway's table), nothing is marked gone")
	require.Equal(t, savesBefore, f.store.saves, "nothing changed, so nothing is written")
}

func TestOtohaDiscoverMediaSkipsGroupsThatCannotHoldTheseAccounts(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.groups[otohaGroupID].Platform = PlatformAnthropic

	report := f.discover(t)

	require.Equal(t, OtohaImageSkippedGroupPlatform, report.SkippedReason)
	require.Empty(t, f.stored(t))
	require.Zero(t, f.accounts.calls, "no account is asked")
}

func TestOtohaDiscoverMediaSourceFailureIsAnError(t *testing.T) {
	f := newOtohaMediaFixture()
	f.accounts.err = errors.New("db down")

	_, err := f.svc.DiscoverMedia(context.Background(), otohaGroupID, OtohaImageTriggerAdmin)

	require.Error(t, err)
}

func TestOtohaDiscoverMediaNeedsADiscoverySource(t *testing.T) {
	f := newOtohaCatalogFixture()

	_, err := f.svc.DiscoverMedia(context.Background(), otohaGroupID, OtohaImageTriggerAdmin)

	require.ErrorIs(t, err, ErrOtohaMediaDiscoveryUnavailable)
}

func TestOtohaMediaDiscoveryRunnerChecksMediaToo(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	runner := NewOtohaImageDiscoveryRunner(f.svc, otohaGroupID)
	runner.delay = time.Millisecond
	runner.Start()
	require.Eventually(t, func() bool {
		report, _ := f.reports.LoadMediaDiscovery(context.Background(), otohaGroupID)
		return report != nil
	}, time.Second, 5*time.Millisecond)
	runner.Stop()

	report, err := f.reports.LoadMediaDiscovery(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaImageTriggerSchedule, report.Trigger)
}

// ---- the catalog the app reads ----

func TestOtohaCatalogMediaIsPricedAsBillingPricesItTimesTheRate(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.discover(t)

	catalog := f.fresh(t)

	require.Len(t, catalog.VideoModels, 2)
	video := catalog.VideoModels[0]
	require.Equal(t, "grok-imagine-video", video.ID)
	require.Equal(t, "Grok Imagine Video", video.Name)
	require.Equal(t, "USD", video.Currency)
	require.Equal(t, "second", video.Per)
	require.Equal(t, []OtohaVideoResolution{{"480p", 0.075}, {"720p", 0.105}}, video.Resolutions, "billing 0.05 and 0.07 a second, rate 1.5; this model has no 1080p")
	video15 := catalog.VideoModels[1]
	require.Equal(t, "grok-imagine-video-1.5", video15.ID)
	require.Equal(t, []OtohaVideoResolution{{"480p", 0.12}, {"720p", 0.21}, {"1080p", 0.375}}, video15.Resolutions)

	require.Len(t, catalog.VoiceModels, 2)
	require.Equal(t, OtohaVoiceModel{
		ID: "tts", Name: "Grok text to speech", API: OtohaVoiceAPI, Endpoint: "POST /v1/tts", App: false,
		Mode: "tts", Currency: "USD", Per: "1M characters", Price: 22.5,
	}, catalog.VoiceModels[0])
	require.Equal(t, "hour", catalog.VoiceModels[1].Per)
	require.Equal(t, 0.15, catalog.VoiceModels[1].Price)

	require.Len(t, catalog.QuickModels, 1)
	require.Equal(t, OtohaQuickModel{
		ID: "jev-latest", Name: "Jev", API: OtohaQuickAPI, Endpoint: "POST /v1/systemone", App: false,
		Currency: "USD", Per: "1M tokens", Billed: "input", Price: 0.063,
	}, catalog.QuickModels[0])
}

func TestOtohaCatalogVideoUsesTheGroupsOwnVideoRateTheOthersDoNot(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.groups[otohaGroupID].VideoRateIndependent = true
	f.groups[otohaGroupID].VideoRateMultiplier = 0.4
	f.discover(t)

	catalog := f.fresh(t)

	require.Equal(t, 0.02, catalog.VideoModels[0].Resolutions[0].Price, "0.05 a second at the video rate 0.4")
	require.Equal(t, 22.5, catalog.VoiceModels[0].Price, "voice follows the group rate, as billing charges it")
	require.Equal(t, 0.063, catalog.QuickModels[0].Price)
}

func TestOtohaCatalogAtAKeyOwnersRatePricesMediaAtThatRate(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.discover(t)

	catalog, err := f.svc.CatalogForGroupAtRate(context.Background(), otohaGroupID, 2)

	require.NoError(t, err)
	require.Equal(t, 0.1, catalog.VideoModels[0].Resolutions[0].Price)
	require.Equal(t, 30.0, catalog.VoiceModels[0].Price)
	require.Equal(t, 0.084, catalog.QuickModels[0].Price)
}

func TestOtohaCatalogLeavesOutMediaWithoutAPriceOrThatTheRouteRefuses(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.discover(t)
	delete(f.pricer.video, "grok-imagine-video-1.5")
	delete(f.pricer.voice, "stt")
	f.routing["quick/jev-latest"] = OtohaCatalogProblemNoAccount

	catalog := f.fresh(t)

	require.Equal(t, []string{"grok-imagine-video"}, mediaIDs(catalog.VideoModels, func(m OtohaVideoModel) string { return m.ID }))
	require.Equal(t, []string{"tts"}, mediaIDs(catalog.VoiceModels, func(m OtohaVoiceModel) string { return m.ID }))
	require.Empty(t, catalog.QuickModels)

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	problems := map[string]string{}
	for _, e := range view.MediaEntries {
		problems[e.Kind+"/"+e.ModelID] = e.Problem
	}
	require.Equal(t, OtohaCatalogProblemNoPrice, problems["video/grok-imagine-video-1.5"])
	require.Equal(t, OtohaCatalogProblemNoPrice, problems["voice/stt"])
	require.Equal(t, OtohaCatalogProblemNoAccount, problems["quick/jev-latest"])
	require.Equal(t, "", problems["video/grok-imagine-video"])
}

func TestOtohaCatalogDropsAVideoResolutionBillingDoesNotPrice(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.discover(t)
	delete(f.pricer.video["grok-imagine-video-1.5"], "1080p")

	catalog := f.fresh(t)

	require.Equal(t, []OtohaVideoResolution{{"480p", 0.12}, {"720p", 0.21}}, catalog.VideoModels[1].Resolutions)
}

func mediaIDs[T any](models []T, id func(T) string) []string {
	out := []string{}
	for _, m := range models {
		out = append(out, id(m))
	}
	return out
}

func TestOtohaCatalogMediaTheGroupsModelListBlocksIsNotOffered(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.discover(t)
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"grok-imagine-video"}}

	catalog := f.fresh(t)

	require.Equal(t, []string{"grok-imagine-video"}, mediaIDs(catalog.VideoModels, func(m OtohaVideoModel) string { return m.ID }))
	require.Empty(t, catalog.QuickModels)
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.True(t, view.GroupModelAllowlist.Enabled)
	require.Equal(t, []string{"grok-imagine-video"}, view.GroupModelAllowlist.Models)
	for _, e := range view.MediaEntries {
		if e.ModelID == "jev-latest" {
			require.Equal(t, OtohaCatalogProblemNotAllowed, e.Problem)
		}
	}
}

func TestOtohaCatalogMediaKeepsTheChatAndImageModelsAsTheyWere(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	before := f.fresh(t)
	f.discover(t)

	after := f.fresh(t)

	require.Equal(t, before.Models, after.Models)
	require.Equal(t, before.ImageModels, after.ImageModels)
	require.NotEqual(t, before.Revision, after.Revision, "the app refreshes when media models come")
}

func TestOtohaCatalogRevisionFollowsAMediaPrice(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.discover(t)
	first := f.fresh(t).Revision

	f.pricer.voice["tts"] = 16

	require.NotEqual(t, first, f.fresh(t).Revision)
}

func TestOtohaCatalogWithoutMediaHasTheSameRevisionAsBefore(t *testing.T) {
	f := newOtohaMediaFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	catalog := f.fresh(t)
	require.Equal(t, otohaCatalogRevision(&OtohaCatalog{Schema: catalog.Schema, Models: catalog.Models}), catalog.Revision,
		"an empty media list is not part of the hash, so a catalog without media keeps the revision an app already holds")

	plain := newOtohaCatalogFixture()
	plain.groups[otohaGroupID].RateMultiplier = 1.5
	plain.groups[otohaGroupID].Platform = PlatformComposite
	plain.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	require.Equal(t, plain.catalog(t, otohaGroupID).Revision, catalog.Revision)
}

func TestOtohaCatalogJSONShapeKeepsTheExistingKeysAndAddsTheMediaArrays(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})
	f.discover(t)

	body, err := json.Marshal(f.fresh(t))
	require.NoError(t, err)
	var shape map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &shape))
	for _, key := range []string{"schema", "revision", "models", "imageModels", "videoModels", "voiceModels", "quickModels"} {
		require.Contains(t, shape, key)
	}
	require.Len(t, shape, 7, "only the three media arrays are new")

	var video []map[string]any
	require.NoError(t, json.Unmarshal(shape["videoModels"], &video))
	require.Equal(t, map[string]any{
		"id": "grok-imagine-video", "name": "Grok Imagine Video", "api": "grok-videos", "endpoint": "POST /v1/videos/generations",
		"app": false, "currency": "USD", "per": "second",
		"resolutions": []any{map[string]any{"resolution": "480p", "price": 0.075}, map[string]any{"resolution": "720p", "price": 0.105}},
	}, video[0])

	var quick []map[string]any
	require.NoError(t, json.Unmarshal(shape["quickModels"], &quick))
	require.Equal(t, map[string]any{
		"id": "jev-latest", "name": "Jev", "api": "typesafe-systemone", "endpoint": "POST /v1/systemone", "app": false,
		"currency": "USD", "per": "1M tokens", "billed": "input", "price": 0.063,
	}, quick[0])
}

func TestOtohaCatalogMediaArraysAreEmptyListsNotNull(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	body, err := json.Marshal(f.catalog(t, otohaGroupID))

	require.NoError(t, err)
	require.Contains(t, string(body), `"videoModels":[]`)
	require.Contains(t, string(body), `"voiceModels":[]`)
	require.Contains(t, string(body), `"quickModels":[]`)
}

func TestOtohaCatalogOfAGroupWithOnlyMediaStillExists(t *testing.T) {
	f := newOtohaMediaFixture(jevListing)
	f.discover(t)

	catalog := f.fresh(t)

	require.NotNil(t, catalog)
	require.Empty(t, catalog.Models)
	require.Len(t, catalog.QuickModels, 1)
}

func TestOtohaAdminViewListsMediaWithPricesAndTheLastCheck(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Empty(t, view.MediaEntries)
	require.Nil(t, view.MediaDiscovery, "no check yet")

	f.discover(t)
	view, err = f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)

	require.NotNil(t, view.MediaDiscovery)
	require.NotNil(t, view.Preview)
	require.Len(t, view.Preview.VideoModels, 2)
	byKey := map[string]OtohaMediaAdminEntry{}
	for _, e := range view.MediaEntries {
		byKey[e.Kind+"/"+e.ModelID] = e
	}
	video := byKey["video/grok-imagine-video"]
	require.True(t, video.InCatalog)
	require.Equal(t, "second", video.Per)
	require.Equal(t, "POST /v1/videos/generations", video.Endpoint)
	require.Equal(t, []OtohaMediaAdminPrice{
		{Label: "480p", Upstream: 0.05, Sale: 0.075},
		{Label: "720p", Upstream: 0.07, Sale: 0.105},
	}, video.Prices)
	tts := byKey["voice/tts"]
	require.Equal(t, []OtohaMediaAdminPrice{{Label: "tts", Upstream: 15, Sale: 22.5}}, tts.Prices)
	jev := byKey["quick/jev-latest"]
	require.Equal(t, []OtohaMediaAdminPrice{{Label: "input", Upstream: 0.042, Sale: 0.063}}, jev.Prices)
}

func TestOtohaMediaIsNotReadWithoutAStore(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true})

	catalog := f.catalog(t, otohaGroupID)

	require.Empty(t, catalog.VideoModels)
	_, err := f.svc.UpdateMediaEntry(context.Background(), otohaGroupID, OtohaMediaEntryInput{Kind: OtohaMediaKindVideo, ModelID: "x"})
	require.ErrorIs(t, err, ErrOtohaMediaDiscoveryUnavailable)
}

func TestOtohaMediaStoreFailureFailsTheCatalogRead(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing)
	f.store.loadErr = errors.New("settings down")

	_, err := f.svc.CatalogForGroup(context.Background(), otohaGroupID)

	require.Error(t, err, "a catalog read that cannot see the media entries must not be kept as the group's catalog")
}

// ---- keeping the entries and the report ----

func TestOtohaMediaSettingsStoreKeepsEntriesPerGroup(t *testing.T) {
	store := NewOtohaMediaStore(&memorySettings{values: map[string]string{}})
	none, err := store.LoadMediaEntries(context.Background(), 5)
	require.NoError(t, err)
	require.Empty(t, none)

	since := discoveryNow
	require.NoError(t, store.SaveMediaEntries(context.Background(), 5, []OtohaMediaEntry{
		{Kind: OtohaMediaKindQuick, ModelID: "jev-latest", Name: "Jev", Enabled: true, SortOrder: 10, ManualFields: []string{"name"}, UnavailableSince: &since},
	}))

	got, err := store.LoadMediaEntries(context.Background(), 5)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "jev-latest", got[0].ModelID)
	require.Equal(t, []string{"name"}, got[0].ManualFields)
	require.True(t, since.Equal(*got[0].UnavailableSince))
	other, err := store.LoadMediaEntries(context.Background(), 6)
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestOtohaMediaSettingsStoreRefusesUnreadableData(t *testing.T) {
	store := NewOtohaMediaStore(&memorySettings{values: map[string]string{"otoha_media_models_5": "{not json"}})

	_, err := store.LoadMediaEntries(context.Background(), 5)

	require.Error(t, err)
}

func TestOtohaMediaDiscoveryStoreKeepsTheLastReportPerGroup(t *testing.T) {
	store := NewOtohaMediaDiscoveryStore(&memorySettings{values: map[string]string{}})
	none, err := store.LoadMediaDiscovery(context.Background(), 5)
	require.NoError(t, err)
	require.Nil(t, none)

	ran := discoveryNow
	require.NoError(t, store.SaveMediaDiscovery(context.Background(), 5, OtohaMediaDiscovery{
		RanAt: &ran, Trigger: OtohaImageTriggerSchedule,
		Candidates: []OtohaMediaDiscoveryCandidate{{Kind: OtohaMediaKindQuick, ModelID: "jev-latest", Result: OtohaImageResultAdded}},
	}))

	got, err := store.LoadMediaDiscovery(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, "jev-latest", got.Candidates[0].ModelID)
	require.NotNil(t, got.Accounts, "lists come back as lists, not null")
	require.NotNil(t, got.Missing)
}

func TestOtohaMediaDiscoveryReportJSONShape(t *testing.T) {
	f := newOtohaMediaFixture(grokOAuthListing, jevListing)
	f.groups[otohaGroupID].Platform = PlatformGrok

	body, err := json.Marshal(f.discover(t))
	require.NoError(t, err)

	var shape map[string]any
	require.NoError(t, json.Unmarshal(body, &shape))
	for _, key := range []string{"ran_at", "trigger", "skipped_reason", "accounts", "candidates", "missing"} {
		require.Contains(t, shape, key)
	}
	candidate := shape["candidates"].([]any)[0].(map[string]any)
	for _, key := range []string{"kind", "model_id", "name", "accounts", "evidence", "declared_by", "result", "reason"} {
		require.Contains(t, candidate, key)
	}
	account := shape["accounts"].([]any)[0].(map[string]any)
	for _, key := range []string{"id", "name", "platform", "type", "listed", "media_models", "error"} {
		require.Contains(t, account, key)
	}
}
