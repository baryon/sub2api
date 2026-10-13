//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrokImageGenerationModelsRequestKeepsHostAndHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://api.x.ai/v1/models", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer xai-test")

	got := grokImageGenerationModelsRequest(req)

	require.Equal(t, "https://api.x.ai/v1/image-generation-models", got.URL.String())
	require.Equal(t, "Bearer xai-test", got.Header.Get("Authorization"))
	require.Equal(t, "https://api.x.ai/v1/models", req.URL.String(), "the original request is not changed")
}

func TestMergeOtohaListedModelsLetsTheImageListWin(t *testing.T) {
	models := []OtohaListedModel{{ID: "grok-4.3", Kind: OtohaListedKindOther}, {ID: "grok-imagine-image"}}
	extra := []OtohaListedModel{{ID: "grok-imagine-image", Kind: OtohaListedKindImage, DeclaredBy: "image_generation_models"}, {ID: "grok-imagine-image-quality", Kind: OtohaListedKindImage}}

	got := byListedID(mergeOtohaListedModels(models, extra))

	require.Len(t, got, 3)
	require.Equal(t, OtohaListedKindImage, got["grok-imagine-image"].Kind)
	require.Equal(t, OtohaListedKindOther, got["grok-4.3"].Kind)
}

type fakeOtohaModelLister map[int64]OtohaAccountListing

func (l fakeOtohaModelLister) ListOtohaModels(_ context.Context, account *Account) ([]OtohaListedModel, string) {
	listing := l[account.ID]
	return listing.Models, listing.Err
}

type fakeOtohaGroupAccounts struct{ AccountRepository }

func (fakeOtohaGroupAccounts) ListByGroup(context.Context, int64) ([]Account, error) {
	return []Account{
		*openAIKeyAccount(1, "gpt", nil),
		*grokAccount(2, "grok", AccountTypeAPIKey),
		{ID: 3, Name: "claude", Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
	}, nil
}

func TestOtohaListingSourceAsksOnlyAccountsThatCanDraw(t *testing.T) {
	source := &otohaAccountListingSource{accounts: fakeOtohaGroupAccounts{}, lister: fakeOtohaModelLister{
		1: {Models: listed("gpt-image-2")},
		2: {Err: OtohaImageListFailed},
	}}

	got, err := source.ListGroupAccountModels(context.Background(), 5)

	require.NoError(t, err)
	require.Len(t, got, 2, "an Anthropic account cannot serve the images endpoints")
	require.Equal(t, "gpt", got[0].Account.Name)
	require.Equal(t, "gpt-image-2", got[0].Models[0].ID)
	require.Equal(t, OtohaImageListFailed, got[1].Err)
}

type memorySettings struct {
	SettingRepository
	values map[string]string
}

func (m *memorySettings) GetValue(_ context.Context, key string) (string, error) {
	v, ok := m.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (m *memorySettings) Set(_ context.Context, key, value string) error {
	m.values[key] = value
	return nil
}

func TestOtohaDiscoveryStoreKeepsTheLastReportPerGroup(t *testing.T) {
	store := NewOtohaImageDiscoveryStore(&memorySettings{values: map[string]string{}})
	none, err := store.LoadImageDiscovery(context.Background(), 5)
	require.NoError(t, err)
	require.Nil(t, none)

	ran := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	require.NoError(t, store.SaveImageDiscovery(context.Background(), 5, OtohaImageDiscovery{
		RanAt: &ran, Trigger: OtohaImageTriggerSchedule,
		Candidates: []OtohaImageDiscoveryCandidate{{ModelID: "gpt-image-2", Result: OtohaImageResultAdded}},
	}))

	got, err := store.LoadImageDiscovery(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", got.Candidates[0].ModelID)
	require.True(t, ran.Equal(*got.RanAt))
	require.NotNil(t, got.Accounts, "lists come back as lists, not null")
	require.NotNil(t, got.Missing)

	other, err := store.LoadImageDiscovery(context.Background(), 6)
	require.NoError(t, err)
	require.Nil(t, other, "another group has its own report")
}

func TestOtohaImageDiscoveryRunnerNeedsAGroup(t *testing.T) {
	runner := NewOtohaImageDiscoveryRunner(nil, 0)
	runner.Start()
	runner.Stop()

	f := newDiscoveryFixture(OtohaAccountListing{Account: openAIKeyAccount(1, "key", nil), Models: listed("gpt-image-2")})
	scheduled := NewOtohaImageDiscoveryRunner(f.svc, otohaGroupID)
	scheduled.delay = time.Millisecond
	scheduled.Start()
	require.Eventually(t, func() bool { return f.listings.callCount() > 0 }, time.Second, 5*time.Millisecond)
	scheduled.Stop()
	report, err := f.reports.LoadImageDiscovery(context.Background(), otohaGroupID)
	require.NoError(t, err)
	require.Equal(t, OtohaImageTriggerSchedule, report.Trigger)
}
