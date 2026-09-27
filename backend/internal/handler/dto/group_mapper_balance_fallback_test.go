package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// How a group bills is for administrators to see; users see only their plan and balance.
func TestGroupMapperExposesBalanceFallbackOnlyToAdmins(t *testing.T) {
	group := &service.Group{
		ID: 8, Name: "otoha", Platform: service.PlatformOpenAI, Status: service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
	}

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "balance_fallback_enabled")

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"balance_fallback_enabled":true`)
}
