//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountGetModelMapping_AntigravityUsesOnlySavedMapping(t *testing.T) {
	account := &Account{
		Platform: PlatformAntigravity,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"gemini-4-argon":      "gemini-4-argon-high",
			"gemini-3.1-pro-high": "gemini-3.1-pro-high",
		}},
	}
	require.True(t, account.IsModelSupported("gemini-3.1-pro-high"))

	// Deleting a saved model must remove it from the cached mapping and routing.
	delete(account.Credentials["model_mapping"].(map[string]any), "gemini-3.1-pro-high")
	require.Equal(t, map[string]string{"gemini-4-argon": "gemini-4-argon-high"}, account.GetModelMapping())
	for _, model := range []string{"gemini-3.1-pro-high", "gemini-3.1-pro-low", "gemini-3.6-flash", "gemini-3.7-flash"} {
		require.False(t, account.IsModelSupported(model), "deleted or unconfigured model %s must stay unavailable", model)
	}
	require.Equal(t, "gemini-4-argon-high", account.GetMappedModel("gemini-4-argon"))
}

func TestDiagnoseModelAvailabilityForPlatform_AntigravityUsesSavedPublicNames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping map[string]any
		want    bool
	}{
		{
			name:    "unconfigured bare name is unavailable",
			mapping: map[string]any{"gemini-3.8-flash-high": "gemini-3.8-flash-high"},
		},
		{
			name: "saved bare name can use its configured variant",
			mapping: map[string]any{
				"gemini-3.8-flash":      "gemini-3.8-flash",
				"gemini-3.8-flash-high": "gemini-3.8-flash-high",
			},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockAccountRepoForPlatform{accounts: []Account{{
				ID: 25, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"model_mapping": tc.mapping},
			}}}
			svc := &GatewayService{accountRepo: repo, cfg: testConfig()}
			diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gemini-3.8-flash", PlatformAntigravity)
			require.True(t, diag.HasAccountsInPool)
			require.Equal(t, tc.want, diag.HasModelSupport)
			if tc.want {
				gateway := &AntigravityGatewayService{}
				require.Equal(t, "gemini-3.8-flash-high", gateway.getMappedModelForThinkingLevel(&repo.accounts[0], "gemini-3.8-flash", "low"))
			}
		})
	}
}
