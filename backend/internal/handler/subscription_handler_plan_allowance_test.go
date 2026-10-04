package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-57: the subscription summary shows the limits that apply to each subscription.
func TestSubscriptionSummaryItemUsesTheSubscriptionsOwnAllowance(t *testing.T) {
	groupDaily, groupMonthly := 5.0, 100.0
	own := 40.0
	group := &service.Group{ID: 7, Name: "Otoha", DailyLimitUSD: &groupDaily, MonthlyLimitUSD: &groupMonthly}

	item := subscriptionSummaryItem(&service.UserSubscription{ID: 1, GroupID: 7, MonthlyUsageUSD: 3, MonthlyLimitUSD: &own, Group: group})
	require.Equal(t, "Otoha", item.GroupName)
	require.InDelta(t, 40, item.MonthlyLimitUSD, 1e-9)
	require.InDelta(t, 5, item.DailyLimitUSD, 1e-9)
	require.InDelta(t, 3, item.MonthlyUsedUSD, 1e-9)

	legacy := subscriptionSummaryItem(&service.UserSubscription{ID: 2, GroupID: 7, Group: group})
	require.InDelta(t, 100, legacy.MonthlyLimitUSD, 1e-9)
}
