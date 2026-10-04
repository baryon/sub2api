package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-57: /v1/usage reports the subscription's own allowance (the plan it was bought with) and computes what is
// left from it; a subscription without one reports the group's limit, as before.
func TestUsageReportsTheSubscriptionsOwnAllowance(t *testing.T) {
	groupMonthly := 100.0
	own := 40.0
	response := usageForFallbackGroup(t, &service.UserSubscription{MonthlyUsageUSD: 5, MonthlyLimitUSD: &own}, 12, &groupMonthly)

	subscription, ok := response["subscription"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 40.0, subscription["monthly_limit_usd"])
	require.Equal(t, 47.0, response["remaining"], "35 left on the plan and 12 in the balance")
}

func TestUsageWithoutAnOwnAllowanceReportsTheGroupsLimit(t *testing.T) {
	groupMonthly := 100.0
	response := usageForFallbackGroup(t, &service.UserSubscription{MonthlyUsageUSD: 5}, 12, &groupMonthly)

	subscription, ok := response["subscription"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 100.0, subscription["monthly_limit_usd"])
	require.Nil(t, subscription["daily_limit_usd"])
	require.Equal(t, 107.0, response["remaining"])
}
