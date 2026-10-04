//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func otohaPortalConfig(portal bool, groupID int64) *config.Config {
	cfg := &config.Config{}
	cfg.Otoha.Portal = portal
	cfg.Otoha.GroupID = groupID
	if groupID > 0 {
		cfg.Otoha.GatewayBaseURL = "https://api.example.com"
	}
	return cfg
}

// The frontend reads otoha_portal_enabled from the public settings (API and the page's injected config) to
// decide whether regular users get the Otoha portal or the usual pages (TASK-61).
func TestSettingService_PublicSettingsReportTheOtohaPortal(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{name: "off by default", cfg: &config.Config{}, want: false},
		{name: "Otoha group without the portal", cfg: otohaPortalConfig(false, 2), want: false},
		{name: "portal without the Otoha group", cfg: otohaPortalConfig(true, 0), want: false},
		{name: "portal on", cfg: otohaPortalConfig(true, 2), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{}}, tc.cfg)
			require.Equal(t, tc.want, svc.OtohaPortalEnabled())

			settings, err := svc.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.want, settings.OtohaPortalEnabled)

			raw, err := svc.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			payload, ok := raw.(*PublicSettingsInjectionPayload)
			require.True(t, ok)
			require.Equal(t, tc.want, payload.OtohaPortalEnabled)
		})
	}
}

func TestSettingService_OtohaPortalEnabledIsNilSafe(t *testing.T) {
	var svc *SettingService
	require.False(t, svc.OtohaPortalEnabled())
	require.False(t, NewSettingService(&settingPublicRepoStub{}, nil).OtohaPortalEnabled())
}
