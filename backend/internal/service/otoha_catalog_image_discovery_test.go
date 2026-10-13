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

// ---- what the upstream lists say ----

func TestOtohaListedModelsXAIModelsListMarksImageModelsByTheirImagePrice(t *testing.T) {
	// GET /v1/models of xAI: language models carry token prices, image models an image_price.
	body := []byte(`{"object":"list","data":[
		{"id":"grok-4.3","object":"model","owned_by":"xai","prompt_text_token_price":12500},
		{"id":"grok-imagine-image","object":"model","owned_by":"xai","image_price":200000000},
		{"id":"grok-imagine-video","object":"model","owned_by":"xai"}]}`)

	got := byListedID(parseOtohaListedModels(body, false))
	require.Equal(t, OtohaListedKindImage, got["grok-imagine-image"].Kind)
	require.Equal(t, "image_price", got["grok-imagine-image"].DeclaredBy)
	require.Empty(t, got["grok-4.3"].Kind, "a token price alone says nothing about the kind")
	require.Empty(t, got["grok-imagine-video"].Kind, "the list gives no kind for it, so it is not taken for an image model")
}

func TestOtohaListedModelsXAIImageGenerationModelsListIsTheTypeListItself(t *testing.T) {
	body := []byte(`{"models":[{"id":"grok-imagine-image-quality","aliases":[],"version":"1.0",
		"input_modalities":["text","image"],"output_modalities":["image"],"image_price":700000000,
		"pricing":[{"quality":"low","resolution":"1k","price_per_image":1},
		           {"quality":"high","resolution":"2k","price_per_image":2},
		           {"quality":"low","resolution":"2k","price_per_image":3}]}]}`)

	got := parseOtohaListedModels(body, true)
	require.Len(t, got, 1)
	require.Equal(t, OtohaListedKindImage, got[0].Kind)
	require.Equal(t, "image_generation_models", got[0].DeclaredBy)
	require.Equal(t, []string{"1k", "2k"}, got[0].Resolutions)
	require.Equal(t, []string{"low", "high"}, got[0].Qualities)
	require.NotNil(t, got[0].ImageInput)
	require.True(t, *got[0].ImageInput, "the list says the model takes an image as input")
}

func TestOtohaListedModelsOutputModalitiesDecideTheKind(t *testing.T) {
	body := []byte(`{"models":[
		{"id":"chat","output_modalities":["text"]},
		{"id":"drawer","output_modalities":["image"]},
		{"id":"router-style","architecture":{"output_modalities":["image","text"]}},
		{"id":"dev-style","modalities":{"input":["text"],"output":["image"]}}]}`)

	got := byListedID(parseOtohaListedModels(body, false))
	require.Equal(t, OtohaListedKindOther, got["chat"].Kind)
	require.Equal(t, OtohaListedKindImage, got["drawer"].Kind)
	require.Equal(t, "output_modalities", got["drawer"].DeclaredBy)
	require.Equal(t, OtohaListedKindImage, got["router-style"].Kind)
	require.Equal(t, OtohaListedKindImage, got["dev-style"].Kind)
}

func TestOtohaListedModelsCapabilityAndTypeFields(t *testing.T) {
	body := []byte(`[
		{"id":"a","capabilities":{"image_generation":true}},
		{"id":"b","capabilities":["chat","image_generation"]},
		{"id":"c","type":"image"},
		{"id":"d","type":"chat"},
		{"id":"e","type":"model"}]`)

	got := byListedID(parseOtohaListedModels(body, false))
	require.Equal(t, OtohaListedKindImage, got["a"].Kind)
	require.Equal(t, "capabilities", got["a"].DeclaredBy)
	require.Equal(t, OtohaListedKindImage, got["b"].Kind)
	require.Equal(t, OtohaListedKindImage, got["c"].Kind)
	require.Equal(t, "type", got["c"].DeclaredBy)
	require.Equal(t, OtohaListedKindOther, got["d"].Kind)
	require.Empty(t, got["e"].Kind, `"model" is the object name, not a kind`)
}

func TestOtohaListedModelsOpenAIListGivesNoKind(t *testing.T) {
	// GET /v1/models of OpenAI: id, object, created, owned_by only.
	body := []byte(`{"object":"list","data":[
		{"id":"gpt-5.4","object":"model","created":1,"owned_by":"system"},
		{"id":"gpt-image-2","object":"model","created":2,"owned_by":"system"}]}`)

	for _, m := range parseOtohaListedModels(body, false) {
		require.Empty(t, m.Kind, m.ID)
	}
}

func byListedID(models []OtohaListedModel) map[string]OtohaListedModel {
	out := map[string]OtohaListedModel{}
	for _, m := range models {
		out[m.ID] = m
	}
	return out
}

// ---- the small table of known image model families ----

func TestOtohaKnownImageFamiliesAreOnlyWhatTheGatewaysImageEndpointServes(t *testing.T) {
	require.NotEmpty(t, otohaKnownImageFamilies)
	for _, family := range otohaKnownImageFamilies {
		require.True(t, isOpenAIImageGenerationModel(family.Prefix+"x"), "%s%s must be accepted by /v1/images", family.Platform, family.Prefix)
	}
	require.True(t, otohaKnownImageFamily(PlatformOpenAI, "gpt-image-2"))
	require.True(t, otohaKnownImageFamily(PlatformGrok, "grok-imagine-image-quality"))
	require.False(t, otohaKnownImageFamily(PlatformGrok, "gpt-image-2"), "a family belongs to its platform")
	require.False(t, otohaKnownImageFamily(PlatformGrok, "grok-imagine-video"))
	require.False(t, otohaKnownImageFamily(PlatformOpenAI, "gpt-5.4"))
}

// ---- candidates from accounts ----

func openAIKeyAccount(id int64, name string, mapping map[string]any) *Account {
	creds := map[string]any{"api_key": "sk-test"}
	if mapping != nil {
		creds["model_mapping"] = mapping
	}
	return &Account{ID: id, Name: name, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: creds}
}

func openAIOAuthAccount(id int64, name string, mapping map[string]any) *Account {
	creds := map[string]any{"access_token": "t"}
	if mapping != nil {
		creds["model_mapping"] = mapping
	}
	return &Account{ID: id, Name: name, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: creds}
}

func grokAccount(id int64, name string, accountType string) *Account {
	return &Account{ID: id, Name: name, Platform: PlatformGrok, Type: accountType, Credentials: map[string]any{"api_key": "xai-test"}}
}

func listed(ids ...string) []OtohaListedModel {
	out := make([]OtohaListedModel, 0, len(ids))
	for _, id := range ids {
		out = append(out, OtohaListedModel{ID: id})
	}
	return out
}

func candidatesByID(candidates []otohaImageCandidate) map[string]otohaImageCandidate {
	out := map[string]otohaImageCandidate{}
	for _, c := range candidates {
		out[c.ModelID] = c
	}
	return out
}

func TestOtohaImageCandidatesFromAPIKeyListWithoutKindUseTheKnownFamilies(t *testing.T) {
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{
		Account: openAIKeyAccount(1, "gpt key", nil),
		Models:  listed("gpt-5.4", "gpt-image-2", "text-embedding-3-large"),
	}}))

	require.Len(t, got, 1)
	c := got["gpt-image-2"]
	require.Equal(t, OtohaImageEvidenceKnownFamily, c.Evidence)
	require.Equal(t, []string{"gpt key"}, c.Accounts)
	require.Equal(t, 1, c.Served)
	require.Equal(t, PlatformOpenAI, c.Platform)
}

func TestOtohaImageCandidateDeclaredByTheListNeedsNoKnownName(t *testing.T) {
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{
		Account: openAIKeyAccount(1, "compatible", nil),
		Models:  []OtohaListedModel{{ID: "flux-2", Name: "Flux 2", Kind: OtohaListedKindImage, DeclaredBy: "output_modalities"}, {ID: "gpt-image-9", Kind: OtohaListedKindOther}},
	}}))

	require.Len(t, got, 1, "a model the list itself calls a text model is not an image model, whatever its name")
	require.Equal(t, OtohaImageEvidenceDeclared, got["flux-2"].Evidence)
	require.Equal(t, "output_modalities", got["flux-2"].DeclaredBy)
	require.Equal(t, "Flux 2", got["flux-2"].Name)
}

func TestOtohaImageCandidatesSkipModelsTheAccountMappingDoesNotServe(t *testing.T) {
	account := openAIKeyAccount(1, "limited", map[string]any{"gpt-5.4": "gpt-5.4"})
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{Account: account, Models: listed("gpt-5.4", "gpt-image-2")}}))

	require.Equal(t, 0, got["gpt-image-2"].Served, "the upstream lists it, but this account does not serve it")
	require.Equal(t, []string{"limited"}, got["gpt-image-2"].Accounts)
}

func TestOtohaImageCandidatesFromAnAliasThatPointsAtAnImageModel(t *testing.T) {
	account := openAIKeyAccount(1, "aliased", map[string]any{"gpt-image-2": "gpt-image-2", "my-draw": "gpt-image-2", "my-chat": "gpt-5.4"})
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{Account: account, Models: listed("gpt-5.4", "gpt-image-2")}}))

	require.Contains(t, got, "gpt-image-2")
	require.Contains(t, got, "my-draw")
	require.NotContains(t, got, "my-chat")
	require.Equal(t, 1, got["my-draw"].Served)
}

func TestOtohaImageCandidatesForChatGPTSignInAccountsComeFromTheGatewaysOwnTable(t *testing.T) {
	// The Codex manifest lists only models that run agents; it never shows image models.
	account := openAIOAuthAccount(7, "chatgpt", nil)
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{Account: account, Models: listed("gpt-5.4")}}))

	require.Contains(t, got, "gpt-image-2")
	require.Equal(t, OtohaImageEvidenceBuiltinCatalog, got["gpt-image-2"].Evidence)
	require.Equal(t, "GPT Image 2", got["gpt-image-2"].Name)
	for id := range got {
		require.True(t, otohaKnownImageFamily(PlatformOpenAI, id), id)
	}

	limited := openAIOAuthAccount(8, "chatgpt limited", map[string]any{"gpt-image-2": "gpt-image-2", "gpt-5.4": "gpt-5.4"})
	only := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{Account: limited, Models: listed("gpt-5.4")}}))
	require.Len(t, only, 1)
	require.Contains(t, only, "gpt-image-2")
}

func TestOtohaImageCandidatesForGrokUseTheListAndFallBackWhenItFails(t *testing.T) {
	ok := grokAccount(2, "grok key", AccountTypeAPIKey)
	listedImage := []OtohaListedModel{{ID: "grok-imagine-image", Kind: OtohaListedKindImage, DeclaredBy: "image_generation_models"}, {ID: "grok-4.3", Kind: OtohaListedKindOther}}
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{Account: ok, Models: listedImage}}))
	require.Len(t, got, 1)
	require.Equal(t, OtohaImageEvidenceDeclared, got["grok-imagine-image"].Evidence)

	failed := grokAccount(3, "grok sign-in", AccountTypeOAuth)
	fallback := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{{Account: failed, Err: OtohaImageListFailed}}))
	require.Contains(t, fallback, "grok-imagine-image")
	require.Equal(t, OtohaImageEvidenceBuiltinCatalog, fallback["grok-imagine-image"].Evidence)
	require.NotContains(t, fallback, "grok-imagine-video", "a video model is not an image model")
}

func TestOtohaImageCandidatesMergeTheSameModelFromSeveralAccounts(t *testing.T) {
	got := candidatesByID(otohaImageCandidatesFrom([]OtohaAccountListing{
		{Account: openAIKeyAccount(1, "key a", nil), Models: listed("gpt-image-2")},
		{Account: openAIKeyAccount(2, "key b", nil), Models: []OtohaListedModel{{ID: "gpt-image-2", Kind: OtohaListedKindImage, DeclaredBy: "type"}}},
	}))

	c := got["gpt-image-2"]
	require.Equal(t, []string{"key a", "key b"}, c.Accounts)
	require.Equal(t, 2, c.Served)
	require.Equal(t, OtohaImageEvidenceDeclared, c.Evidence, "what the list declares outweighs a name match")
}

// ---- the plan ----

var discoveryNow = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

func noReason(otohaImageCandidate) string { return "" }

func plan(candidates []otohaImageCandidate, existing []OtohaImageEntry, allListed bool, reason func(otohaImageCandidate) string) otohaImagePlan {
	if reason == nil {
		reason = noReason
	}
	return planOtohaImageDiscovery(otohaImagePlanInput{Candidates: candidates, Existing: existing, AllListed: allListed, Reason: reason, Now: discoveryNow})
}

func TestOtohaImagePlanCreatesAutoEntriesWithPlatformDefaultsAndNoPrice(t *testing.T) {
	out := plan([]otohaImageCandidate{
		{ModelID: "gpt-image-2", Name: "GPT Image 2", Platform: PlatformOpenAI, Accounts: []string{"a"}, Served: 1, Evidence: OtohaImageEvidenceKnownFamily},
		{ModelID: "grok-imagine-image", Platform: PlatformGrok, Accounts: []string{"g"}, Served: 1, Evidence: OtohaImageEvidenceDeclared},
	}, nil, true, nil)

	require.Len(t, out.Create, 2)
	gpt := out.Create[0]
	require.Equal(t, "gpt-image-2", gpt.ModelID)
	require.Equal(t, OtohaImageSourceAuto, gpt.Source)
	require.True(t, gpt.Enabled)
	require.Equal(t, "GPT Image 2", gpt.Name)
	require.Equal(t, []string{"1024x1024", "1536x1024", "1024x1536"}, gpt.Sizes)
	require.Equal(t, []string{"low", "medium", "high"}, gpt.Qualities)
	require.True(t, gpt.Edits)
	require.Nil(t, gpt.Price1K, "the price follows billing, so it can never differ from what is charged")
	require.Nil(t, gpt.Price2K)
	require.Nil(t, gpt.Price4K)

	grok := out.Create[1]
	require.Equal(t, "grok-imagine-image", grok.Name, "no name in the list: the model ID")
	require.Equal(t, []string{"1024x1024"}, grok.Sizes)
	require.Empty(t, grok.Qualities)
	require.False(t, grok.Edits)
	require.Greater(t, grok.SortOrder, 0)
	require.Greater(t, out.Create[1].SortOrder, out.Create[0].SortOrder)

	for _, c := range out.Candidates {
		require.Equal(t, OtohaImageResultAdded, c.Result, c.ModelID)
	}
}

func TestOtohaImagePlanTakesSizesQualitiesAndEditsFromTheListWhenItSaysSo(t *testing.T) {
	yes := true
	out := plan([]otohaImageCandidate{{
		ModelID: "grok-imagine-image-quality", Name: "Grok Imagine Quality", Platform: PlatformGrok, Served: 1, Evidence: OtohaImageEvidenceDeclared,
		Resolutions: []string{"1k", "1.5k", "2k"}, Qualities: []string{"low", "high"}, Edits: &yes,
	}}, nil, true, nil)

	e := out.Create[0]
	require.Equal(t, []string{"1024x1024", "1536x1536", "2048x2048"}, e.Sizes)
	require.Equal(t, []string{"low", "high"}, e.Qualities)
	require.True(t, e.Edits)
	for _, size := range e.Sizes {
		_, ok := ClassifyImageBillingTier(size)
		require.True(t, ok, size)
	}
}

func TestOtohaImagePlanKeepsEveryValueTheAdminChanged(t *testing.T) {
	existing := []OtohaImageEntry{{
		ID: 5, GroupID: 1, ModelID: "gpt-image-2", Name: "Draw", Enabled: false, Sizes: []string{"3840x2160"},
		Qualities: []string{"high"}, Edits: false, Price1K: otohaFloat(0.5), SortOrder: 20,
		Source: OtohaImageSourceAuto, ManualFields: []string{"name", "sizes", "enabled", "price_1k"},
	}}
	out := plan([]otohaImageCandidate{{ModelID: "gpt-image-2", Name: "GPT Image 2", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}}, existing, true, nil)

	require.Empty(t, out.Create)
	require.Len(t, out.Update, 1)
	got := out.Update[0]
	require.Equal(t, "Draw", got.Name, "the admin renamed it")
	require.Equal(t, []string{"3840x2160"}, got.Sizes, "the admin chose the sizes")
	require.False(t, got.Enabled)
	require.Equal(t, 0.5, *got.Price1K)
	require.Equal(t, []string{"low", "medium", "high"}, got.Qualities, "qualities are not among the admin's changes, so they follow the upstream")
}

func TestOtohaImagePlanRefreshesTheFieldsTheAdminDidNotChange(t *testing.T) {
	existing := []OtohaImageEntry{{
		ID: 5, GroupID: 1, ModelID: "gpt-image-2", Name: "old name", Enabled: true, Sizes: []string{"1024x1024"},
		Qualities: []string{"low"}, Edits: false, Source: OtohaImageSourceAuto, ManualFields: []string{"name"},
	}}
	out := plan([]otohaImageCandidate{{ModelID: "gpt-image-2", Name: "GPT Image 2", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}}, existing, true, nil)

	require.Len(t, out.Update, 1)
	got := out.Update[0]
	require.Equal(t, "old name", got.Name)
	require.Equal(t, []string{"1024x1024", "1536x1024", "1024x1536"}, got.Sizes)
	require.Equal(t, []string{"low", "medium", "high"}, got.Qualities)
	require.True(t, got.Edits)
	require.Equal(t, OtohaImageResultUpdated, out.Candidates[0].Result)
}

func TestOtohaImagePlanLeavesAnEntryTheAdminAddedByHandAlone(t *testing.T) {
	existing := []OtohaImageEntry{{ID: 5, GroupID: 1, ModelID: "gpt-image-2", Name: "Mine", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceManual}}
	out := plan([]otohaImageCandidate{{ModelID: "gpt-image-2", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}}, existing, true, nil)

	require.Empty(t, out.Create)
	require.Empty(t, out.Update)
	require.Equal(t, OtohaImageResultManualKept, out.Candidates[0].Result)
}

func TestOtohaImagePlanUnchangedWhenNothingDiffers(t *testing.T) {
	first := plan([]otohaImageCandidate{{ModelID: "gpt-image-2", Name: "GPT Image 2", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}}, nil, true, nil)
	stored := first.Create[0]
	stored.ID = 9
	second := plan([]otohaImageCandidate{{ModelID: "gpt-image-2", Name: "GPT Image 2", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}}, []OtohaImageEntry{stored}, true, nil)

	require.Empty(t, second.Create)
	require.Empty(t, second.Update)
	require.Equal(t, OtohaImageResultUnchanged, second.Candidates[0].Result)
}

func TestOtohaImagePlanMarksAnAutoEntryUnavailableWhenEveryAccountListedAndNoneHasIt(t *testing.T) {
	existing := []OtohaImageEntry{
		{ID: 1, GroupID: 1, ModelID: "gone", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceAuto},
		{ID: 2, GroupID: 1, ModelID: "by-hand", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceManual},
		{ID: 3, GroupID: 1, ModelID: "still-there", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceAuto},
	}
	out := plan([]otohaImageCandidate{{ModelID: "still-there", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily}}, existing, true, nil)

	require.Equal(t, []string{"gone"}, out.Missing)
	var gone *OtohaImageEntry
	for i := range out.Update {
		if out.Update[i].ModelID == "gone" {
			gone = &out.Update[i]
		}
		require.NotEqual(t, "by-hand", out.Update[i].ModelID, "an entry the admin added is not the discovery's to judge")
	}
	require.NotNil(t, gone)
	require.NotNil(t, gone.UnavailableSince)
	require.Equal(t, discoveryNow, *gone.UnavailableSince)
}

func TestOtohaImagePlanDoesNotJudgeAvailabilityWhenAnAccountCouldNotBeListed(t *testing.T) {
	existing := []OtohaImageEntry{{ID: 1, GroupID: 1, ModelID: "gone", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceAuto}}
	out := plan(nil, existing, false, nil)

	require.Empty(t, out.Missing)
	require.Empty(t, out.Update)
}

func TestOtohaImagePlanKeepsTheFirstMissingTimeAndRestoresWhenItIsBack(t *testing.T) {
	since := discoveryNow.Add(-48 * time.Hour)
	existing := []OtohaImageEntry{
		{ID: 1, GroupID: 1, ModelID: "gone", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceAuto, UnavailableSince: &since},
		{ID: 2, GroupID: 1, ModelID: "back", Name: "back", Enabled: true, Sizes: []string{"1024x1024"}, Source: OtohaImageSourceAuto, UnavailableSince: &since},
	}
	out := plan([]otohaImageCandidate{{ModelID: "back", Name: "back", Platform: PlatformGrok, Served: 1, Evidence: OtohaImageEvidenceDeclared}}, existing, true, nil)

	require.Equal(t, []string{"gone"}, out.Missing)
	for _, e := range out.Update {
		require.NotEqual(t, "gone", e.ModelID, "already unavailable: nothing to write")
		if e.ModelID == "back" {
			require.Nil(t, e.UnavailableSince)
		}
	}
	require.Equal(t, OtohaImageResultRestored, out.Candidates[0].Result)
}

func TestOtohaImagePlanReportsWhyACandidateWasNotAdded(t *testing.T) {
	reasons := map[string]string{"dall-e-3": OtohaImageReasonGatewayNotSupported, "gpt-image-2": OtohaCatalogProblemNoAccount}
	out := plan([]otohaImageCandidate{
		{ModelID: "dall-e-3", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceDeclared},
		{ModelID: "gpt-image-2", Platform: PlatformOpenAI, Served: 1, Evidence: OtohaImageEvidenceKnownFamily},
		{ModelID: "grok-imagine-image", Platform: PlatformGrok, Served: 1, Evidence: OtohaImageEvidenceDeclared},
	}, nil, true, func(c otohaImageCandidate) string { return reasons[c.ModelID] })

	require.Len(t, out.Create, 1)
	require.Equal(t, "grok-imagine-image", out.Create[0].ModelID)
	got := map[string]OtohaImageDiscoveryCandidate{}
	for _, c := range out.Candidates {
		got[c.ModelID] = c
	}
	require.Equal(t, OtohaImageResultNotAdded, got["dall-e-3"].Result)
	require.Equal(t, OtohaImageReasonGatewayNotSupported, got["dall-e-3"].Reason)
	require.Equal(t, OtohaCatalogProblemNoAccount, got["gpt-image-2"].Reason)
	require.Equal(t, OtohaImageResultAdded, got["grok-imagine-image"].Result)
}

func TestOtohaImagePlanCandidatesNoAccountServesAreNotAdded(t *testing.T) {
	out := plan([]otohaImageCandidate{{ModelID: "gpt-image-2", Platform: PlatformOpenAI, Accounts: []string{"limited"}, Served: 0, Evidence: OtohaImageEvidenceKnownFamily}}, nil, true, nil)

	require.Empty(t, out.Create)
	require.Equal(t, OtohaImageResultNotAdded, out.Candidates[0].Result)
	require.Equal(t, OtohaCatalogProblemNoAccount, out.Candidates[0].Reason)
}

// ---- the service ----

type fakeOtohaImageListings struct {
	mu       sync.Mutex
	listings []OtohaAccountListing
	err      error
	calls    int
}

func (l *fakeOtohaImageListings) ListGroupAccountModels(context.Context, int64) ([]OtohaAccountListing, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.listings, l.err
}

func (l *fakeOtohaImageListings) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

type fakeOtohaImageReports struct {
	mu      sync.Mutex
	reports map[int64]OtohaImageDiscovery
}

func (s *fakeOtohaImageReports) LoadImageDiscovery(_ context.Context, groupID int64) (*OtohaImageDiscovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report, ok := s.reports[groupID]
	if !ok {
		return nil, nil
	}
	return &report, nil
}

func (s *fakeOtohaImageReports) SaveImageDiscovery(_ context.Context, groupID int64, report OtohaImageDiscovery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reports == nil {
		s.reports = map[int64]OtohaImageDiscovery{}
	}
	s.reports[groupID] = report
	return nil
}

type discoveryFixture struct {
	*otohaImageFixture
	listings *fakeOtohaImageListings
	reports  *fakeOtohaImageReports
}

func newDiscoveryFixture(listings ...OtohaAccountListing) *discoveryFixture {
	f := &discoveryFixture{otohaImageFixture: newOtohaImageFixture(), listings: &fakeOtohaImageListings{listings: listings}, reports: &fakeOtohaImageReports{}}
	f.groups[otohaGroupID].Platform = PlatformOpenAI
	f.groups[otohaGroupID].AllowImageGeneration = false
	f.svc.WithDiscovery(f.listings, f.reports)
	f.svc.now = func() time.Time { return discoveryNow }
	return f
}

func (f *discoveryFixture) discover(t *testing.T) *OtohaImageDiscovery {
	t.Helper()
	report, err := f.svc.DiscoverImages(context.Background(), otohaGroupID, OtohaImageTriggerAdmin)
	require.NoError(t, err)
	return report
}

func (f *discoveryFixture) entries(t *testing.T) map[string]OtohaImageEntry {
	t.Helper()
	list, err := f.images.ListImagesByGroup(context.Background(), otohaGroupID)
	require.NoError(t, err)
	out := map[string]OtohaImageEntry{}
	for _, e := range list {
		out[e.ModelID] = e
	}
	return out
}

func TestOtohaDiscoverImagesCreatesEntriesPricedByBillingTimesTheGroupRate(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "gpt key", nil), Models: listed("gpt-5.4", "gpt-image-2")})
	f.groups[otohaGroupID].RateMultiplier = 1.5
	f.groups[otohaGroupID].AllowImageGeneration = true

	report := f.discover(t)

	require.Equal(t, OtohaImageTriggerAdmin, report.Trigger)
	require.Equal(t, discoveryNow, *report.RanAt)
	require.Len(t, report.Candidates, 1)
	require.Equal(t, OtohaImageResultAdded, report.Candidates[0].Result)
	require.Contains(t, f.entries(t), "gpt-image-2")

	model := f.catalog(t, otohaGroupID).ImageModels[0]
	require.Equal(t, "gpt-image-2", model.ID)
	require.Equal(t, 0.06, model.Sizes[0].Price, "billing 0.04 for 1K, group rate 1.5")
	require.Equal(t, 0.09, model.Sizes[1].Price, "billing 0.06 for 2K, group rate 1.5")
}

func TestOtohaDiscoverImagesStaysOutOfTheCatalogUntilTheGroupAllowsDrawing(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "gpt key", nil), Models: listed("gpt-image-2")})
	f.discover(t)

	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.False(t, view.GroupAllowImageGeneration)
	require.Equal(t, OtohaCatalogProblemImagesNotAllowed, view.ImageEntries[0].Problem)
	require.Empty(t, f.catalog(t, otohaGroupID).ImageModels, "the group's switch stays the admin's: discovery does not flip it")
	require.False(t, f.groups[otohaGroupID].AllowImageGeneration)
	require.NotNil(t, view.ImageDiscovery)
	require.Equal(t, "gpt-image-2", view.ImageDiscovery.Candidates[0].ModelID)
}

func TestOtohaDiscoverImagesReportsAccountsAndWhySomethingWasNotAdded(t *testing.T) {
	f := newDiscoveryFixture(
		OtohaAccountListing{Account: openAIKeyAccount(1, "compatible", nil), Models: []OtohaListedModel{{ID: "dall-e-3", Kind: OtohaListedKindImage, DeclaredBy: "type"}, {ID: "gpt-image-2"}}},
		OtohaAccountListing{Account: openAIKeyAccount(2, "down", nil), Err: OtohaImageListFailed},
	)
	f.groups[otohaGroupID].ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"dall-e-3"}}

	report := f.discover(t)

	reasons := map[string]string{}
	for _, c := range report.Candidates {
		reasons[c.ModelID] = c.Reason
	}
	require.Equal(t, OtohaImageReasonGatewayNotSupported, reasons["dall-e-3"], "the gateway's image endpoints do not serve it")
	require.Equal(t, OtohaImageReasonBlockedByGroupModels, reasons["gpt-image-2"])
	require.Len(t, report.Accounts, 2)
	require.Equal(t, 2, report.Accounts[0].Listed)
	require.Equal(t, 2, report.Accounts[0].ImageModels, "dall-e-3 is declared by the list, gpt-image-2 matches a known family")
	require.Equal(t, OtohaImageListFailed, report.Accounts[1].Error)
	require.Empty(t, f.entries(t))
}

func TestOtohaDiscoverImagesUsesTheImageRoute(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")})
	f.routing["gpt-image-2"] = OtohaCatalogProblemChannelRestricted

	report := f.discover(t)

	require.Equal(t, OtohaImageResultNotAdded, report.Candidates[0].Result)
	require.Equal(t, OtohaCatalogProblemChannelRestricted, report.Candidates[0].Reason)
}

func TestOtohaDiscoverImagesSkipsGroupsThatCannotDraw(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")})
	f.groups[otohaGroupID].Platform = PlatformAnthropic

	report := f.discover(t)

	require.Equal(t, OtohaImageSkippedGroupPlatform, report.SkippedReason)
	require.Equal(t, 0, f.listings.callCount(), "no account is asked")
	require.Empty(t, f.entries(t))
}

func TestOtohaDiscoverImagesWithoutAccountsSaysSo(t *testing.T) {
	f := newDiscoveryFixture()
	report := f.discover(t)
	require.Equal(t, OtohaImageSkippedNoAccounts, report.SkippedReason)
}

func TestOtohaDiscoverImagesAnAdminEditSticksThroughTheNextRefresh(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")})
	f.discover(t)
	stored := f.entries(t)["gpt-image-2"]

	_, err := f.svc.UpdateImageEntry(context.Background(), otohaGroupID, stored.ID, OtohaImageEntryInput{
		ModelID: "gpt-image-2", Name: "Draw it", Enabled: stored.Enabled, Sizes: []string{"1024x1024", "3840x2160"},
		Qualities: stored.Qualities, Edits: stored.Edits, Price1K: otohaFloat(0.07),
	})
	require.NoError(t, err)
	edited := f.entries(t)["gpt-image-2"]
	require.Equal(t, OtohaImageSourceAuto, edited.Source)
	require.ElementsMatch(t, []string{"name", "sizes", "price_1k"}, edited.ManualFields)

	report := f.discover(t)

	after := f.entries(t)["gpt-image-2"]
	require.Equal(t, "Draw it", after.Name)
	require.Equal(t, []string{"1024x1024", "3840x2160"}, after.Sizes)
	require.Equal(t, 0.07, *after.Price1K)
	require.Equal(t, OtohaImageResultUnchanged, report.Candidates[0].Result)
}

func TestOtohaDiscoverImagesMarksVanishedModelsUnavailableAndRestoresThem(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2", "gpt-image-1")})
	f.groups[otohaGroupID].AllowImageGeneration = true
	f.pricer["gpt-image-1"] = map[string]float64{"1K": 0.04, "2K": 0.06, "4K": 0.12}
	f.discover(t)
	require.Len(t, f.catalog(t, otohaGroupID).ImageModels, 2)

	f.listings.listings = []OtohaAccountListing{{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")}}
	report := f.discover(t)

	require.Equal(t, []string{"gpt-image-1"}, report.Missing)
	require.NotNil(t, f.entries(t)["gpt-image-1"].UnavailableSince)
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	problems := map[string]string{}
	for _, e := range view.ImageEntries {
		problems[e.ModelID] = e.Problem
	}
	require.Equal(t, OtohaCatalogProblemUpstreamMissing, problems["gpt-image-1"])
	require.Empty(t, problems["gpt-image-2"])
	ids := []string{}
	for _, m := range f.catalog(t, otohaGroupID).ImageModels {
		ids = append(ids, m.ID)
	}
	require.Equal(t, []string{"gpt-image-2"}, ids, "the app no longer gets a model the upstream does not offer")

	f.listings.listings = []OtohaAccountListing{{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2", "gpt-image-1")}}
	back := f.discover(t)
	require.Nil(t, f.entries(t)["gpt-image-1"].UnavailableSince)
	require.Empty(t, back.Missing)
	require.Len(t, f.catalog(t, otohaGroupID).ImageModels, 2)
}

func TestOtohaDiscoverImagesFailedListingChangesNothingAndKeepsTheLastReport(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")})
	f.discover(t)

	f.listings.listings = []OtohaAccountListing{{Account: openAIKeyAccount(1, "key", nil), Err: OtohaImageListFailed}}
	report := f.discover(t)

	require.Nil(t, f.entries(t)["gpt-image-2"].UnavailableSince, "an upstream that could not be asked has not withdrawn the model")
	require.Equal(t, OtohaImageListFailed, report.Accounts[0].Error)
}

func TestOtohaDiscoverImagesSourceFailureIsAnError(t *testing.T) {
	f := newDiscoveryFixture()
	f.listings.err = errors.New("db down")
	_, err := f.svc.DiscoverImages(context.Background(), otohaGroupID, OtohaImageTriggerAdmin)
	require.Error(t, err)
}

func TestOtohaDiscoverImagesNeedsADiscoverySource(t *testing.T) {
	f := newOtohaImageFixture()
	_, err := f.svc.DiscoverImages(context.Background(), otohaGroupID, OtohaImageTriggerAdmin)
	require.ErrorIs(t, err, ErrOtohaImageDiscoveryUnavailable)
}

func TestOtohaImageDiscoveryReportJSONShape(t *testing.T) {
	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")})
	f.discover(t)
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)

	body, err := json.Marshal(view.ImageDiscovery)
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(body, &generic))
	for _, key := range []string{"ran_at", "trigger", "skipped_reason", "accounts", "candidates", "missing"} {
		require.Contains(t, generic, key)
	}
	candidate := generic["candidates"].([]any)[0].(map[string]any)
	for _, key := range []string{"model_id", "name", "accounts", "evidence", "declared_by", "result", "reason"} {
		require.Contains(t, candidate, key)
	}
	require.Equal(t, []any{}, generic["missing"], "a list, never null")
}

func TestOtohaAdminViewHasNoDiscoveryBeforeTheFirstRun(t *testing.T) {
	f := newDiscoveryFixture()
	view, err := f.svc.AdminView(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Nil(t, view.ImageDiscovery)
}
