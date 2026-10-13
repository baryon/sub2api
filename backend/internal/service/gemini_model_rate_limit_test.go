//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TASK-85: a Gemini API key's 429 names one model (the free tier has no quota for a Pro model, a daily quota of one
// model is used up). Quotas of the Gemini API are per model, so only that model cools down on the account.

type geminiModelLimitRepo struct {
	mockAccountRepoForGemini
	accountLimits []time.Time
	modelScopes   []string
	modelResets   []time.Time
	modelReasons  []string
}

func (r *geminiModelLimitRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.accountLimits = append(r.accountLimits, resetAt)
	return nil
}

func (r *geminiModelLimitRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, resetAt time.Time, reason ...string) error {
	r.modelScopes = append(r.modelScopes, scope)
	r.modelResets = append(r.modelResets, resetAt)
	if len(reason) > 0 {
		r.modelReasons = append(r.modelReasons, reason[0])
	}
	return nil
}

func newGeminiModelLimitService() (*GeminiMessagesCompatService, *geminiModelLimitRepo) {
	repo := &geminiModelLimitRepo{}
	return &GeminiMessagesCompatService{
		accountRepo:      repo,
		rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
	}, repo
}

const geminiFreeTierNoQuotaBody = `{"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_requests","quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier","quotaDimensions":{"location":"global","model":"gemini-3.1-pro-preview"},"quotaValue":"0"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"34s"}]}}`

func TestGeminiAPIKey429CoolsDownOnlyTheModel(t *testing.T) {
	svc, repo := newGeminiModelLimitService()
	account := &Account{ID: 12, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	before := time.Now()
	svc.handleGeminiUpstreamErrorForModel(context.Background(), account, "gemini-3.1-pro-preview", http.StatusTooManyRequests, http.Header{}, []byte(geminiFreeTierNoQuotaBody))

	require.Empty(t, repo.accountLimits, "the account itself stays available")
	require.Equal(t, []string{"gemini-3.1-pro-preview"}, repo.modelScopes)
	require.WithinDuration(t, before.Add(34*time.Second), repo.modelResets[0], 3*time.Second, "the upstream's own wait is used")

	require.False(t, account.IsSchedulableForModel("gemini-3.1-pro-preview"), "the limited model is not scheduled on it")
	require.True(t, account.IsSchedulableForModel("gemini-3.5-flash"), "another model still is")
	require.True(t, account.IsSchedulable(), "the account has no account-level limit")
}

func TestGeminiAPIKey429WithoutResetTimeCoolsOnlyTheModelUntilPSTMidnight(t *testing.T) {
	svc, repo := newGeminiModelLimitService()
	account := &Account{ID: 12, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	body := []byte(`{"error":{"code":429,"message":"Quota exceeded for metric: generativelanguage.googleapis.com/generate_content_free_tier_requests, limit: 25 per day per model","status":"RESOURCE_EXHAUSTED"}}`)

	svc.handleGeminiUpstreamErrorForModel(context.Background(), account, "gemini-3.5-flash", http.StatusTooManyRequests, http.Header{}, body)

	require.Empty(t, repo.accountLimits)
	require.Equal(t, []string{"gemini-3.5-flash"}, repo.modelScopes)
	require.WithinDuration(t, time.Unix(*nextGeminiDailyResetUnix(), 0), repo.modelResets[0], 5*time.Second)
	require.False(t, account.IsSchedulableForModel("gemini-3.5-flash"))
	require.True(t, account.IsSchedulableForModel("gemini-3.1-flash-image"))
}

func TestGeminiAPIKey429UsesTheModelTheAccountMapsTo(t *testing.T) {
	svc, repo := newGeminiModelLimitService()
	account := &Account{
		ID: 12, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"otoha-pro": "gemini-3.1-pro-preview"}},
	}

	svc.handleGeminiUpstreamErrorForModel(context.Background(), account, "otoha-pro", http.StatusTooManyRequests, http.Header{}, []byte(geminiFreeTierNoQuotaBody))

	require.Equal(t, []string{"gemini-3.1-pro-preview"}, repo.modelScopes, "the scope is the model the scheduler looks up")
	require.False(t, account.IsSchedulableForModel("otoha-pro"))
	require.True(t, account.IsSchedulableForModel("gemini-3.5-flash"))
}

func TestGeminiThirdPartyAPIKey429CoolsOnlyTheModel(t *testing.T) {
	svc, repo := newGeminiModelLimitService()
	account := &Account{
		ID: 13, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://relay.example.com"},
	}
	body := []byte(`{"error":{"code":429,"message":"rate limited"}}`)

	before := time.Now()
	svc.handleGeminiUpstreamErrorForModel(context.Background(), account, "gemini-3.5-flash", http.StatusTooManyRequests, http.Header{}, body)

	require.Empty(t, repo.accountLimits)
	require.Equal(t, []string{"gemini-3.5-flash"}, repo.modelScopes)
	require.WithinDuration(t, before.Add(defaultRateLimit429CooldownSeconds*time.Second), repo.modelResets[0], 2*time.Second)
}

// Accounts whose quota is not per model keep the account-level cooldown, and so does a failure with no model known.
func TestGeminiRateLimitStaysAccountWideWhereQuotaIsNotPerModel(t *testing.T) {
	body := []byte(geminiFreeTierNoQuotaBody)
	cases := []struct {
		name    string
		account *Account
		model   string
	}{
		{"google one oauth", &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"oauth_type": "google_one", "tier_id": "google_ai_pro"}}, "gemini-3.1-pro-preview"},
		{"code assist oauth", &Account{ID: 2, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"oauth_type": "code_assist", "project_id": "p"}}, "gemini-3.1-pro-preview"},
		{"vertex service account", &Account{ID: 3, Platform: PlatformGemini, Type: AccountTypeServiceAccount, Credentials: map[string]any{"project_id": "p"}}, "gemini-3.1-pro-preview"},
		{"api key without a model", &Account{ID: 4, Platform: PlatformGemini, Type: AccountTypeAPIKey}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newGeminiModelLimitService()
			svc.handleGeminiUpstreamErrorForModel(context.Background(), tc.account, tc.model, http.StatusTooManyRequests, http.Header{}, body)
			require.Len(t, repo.accountLimits, 1)
			require.Empty(t, repo.modelScopes)
		})
	}
}

func TestGeminiPoolModeAPIKey429StaysUnmarkedWithAModel(t *testing.T) {
	svc, repo := newGeminiModelLimitService()
	account := &Account{ID: 14, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}

	svc.handleGeminiUpstreamErrorForModel(context.Background(), account, "gemini-3.5-flash", http.StatusTooManyRequests, http.Header{}, []byte(geminiFreeTierNoQuotaBody))

	require.Empty(t, repo.accountLimits)
	require.Empty(t, repo.modelScopes)
}
