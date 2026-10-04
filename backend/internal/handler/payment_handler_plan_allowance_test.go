//go:build unit

package handler

import (
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-57: the plans offered at checkout (and on the Otoha buy page) show each plan's own allowance where it has
// one and the group's limit otherwise, so Plus, Pro and Max on one group no longer look the same.
func TestCheckoutPlanShowsEachPlansOwnAllowance(t *testing.T) {
	groupDaily, groupMonthly := 5.0, 100.0
	gi := service.PlanGroupInfo{Name: "Otoha", DailyLimitUSD: &groupDaily, MonthlyLimitUSD: &groupMonthly}
	plus, max := 40.0, 400.0

	got := checkoutPlanFromPlan(&dbent.SubscriptionPlan{ID: 1, GroupID: 7, Name: "Plus", Price: 20, MonthlyLimitUsd: &plus}, gi)
	require.InDelta(t, 40, *got.MonthlyLimitUSD, 1e-9)
	require.InDelta(t, 5, *got.DailyLimitUSD, 1e-9, "no daily allowance of its own: the group's")
	require.Nil(t, got.WeeklyLimitUSD)
	require.Equal(t, "Plus", got.Name)
	require.Equal(t, "Otoha", got.GroupName)

	got = checkoutPlanFromPlan(&dbent.SubscriptionPlan{ID: 3, GroupID: 7, Name: "Max", Price: 150, MonthlyLimitUsd: &max}, gi)
	require.InDelta(t, 400, *got.MonthlyLimitUSD, 1e-9)

	legacy := checkoutPlanFromPlan(&dbent.SubscriptionPlan{ID: 9, GroupID: 7, Name: "Old", Price: 10}, gi)
	require.Same(t, gi.MonthlyLimitUSD, legacy.MonthlyLimitUSD, "a plan without an allowance shows the group's, as before")
	require.Same(t, gi.DailyLimitUSD, legacy.DailyLimitUSD)
}
