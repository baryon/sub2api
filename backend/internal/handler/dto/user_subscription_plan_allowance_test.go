package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-57: the subscription views (user and admin) get the limits that apply to the subscription — its own
// allowance where it has one, the group's otherwise — and the plan it was bought with.
func TestUserSubscriptionCarriesTheLimitsThatApply(t *testing.T) {
	groupDaily, groupMonthly := 5.0, 100.0
	own := 40.0
	planID := int64(3)
	sub := &service.UserSubscription{
		ID:              1,
		PlanID:          &planID,
		MonthlyLimitUSD: &own,
		Group:           &service.Group{ID: 7, Name: "Otoha", DailyLimitUSD: &groupDaily, MonthlyLimitUSD: &groupMonthly},
	}

	out := UserSubscriptionFromService(sub)
	require.NotNil(t, out.PlanID)
	require.Equal(t, planID, *out.PlanID)
	require.InDelta(t, 40, *out.MonthlyLimitUSD, 1e-9)
	require.InDelta(t, 5, *out.DailyLimitUSD, 1e-9)
	require.Nil(t, out.WeeklyLimitUSD)
	require.InDelta(t, 100, *out.Group.MonthlyLimitUSD, 1e-9, "the group keeps its own limit")

	admin := UserSubscriptionFromServiceAdmin(sub)
	require.InDelta(t, 40, *admin.MonthlyLimitUSD, 1e-9)

	legacy := UserSubscriptionFromService(&service.UserSubscription{ID: 2, Group: sub.Group})
	require.Nil(t, legacy.PlanID)
	require.InDelta(t, 100, *legacy.MonthlyLimitUSD, 1e-9, "without its own allowance: the group's")

	body, err := json.Marshal(UserSubscriptionFromService(&service.UserSubscription{ID: 4}))
	require.NoError(t, err)
	require.Contains(t, string(body), `"monthly_limit_usd":null`)
	require.NotContains(t, string(body), `"plan_id"`)
}
