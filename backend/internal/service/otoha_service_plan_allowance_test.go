//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TASK-57: the Otoha account page shows the allowance of the plan the user bought (not the group's) and which
// plan that is, so the buy page can tell a renewal from an upgrade.
func TestOtohaAccountSummaryShowsThePlansOwnAllowance(t *testing.T) {
	f := newOtohaFixture(t)
	windowStart := f.now.Add(-3 * 24 * time.Hour)
	planID := int64(5)
	own := 120.0
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		StartsAt: windowStart, ExpiresAt: f.now.Add(27 * 24 * time.Hour), MonthlyWindowStart: &windowStart, MonthlyUsageUSD: 12.25,
		PlanID: &planID, MonthlyLimitUSD: &own}
	f.repo.planOrder = &OtohaPlanOrder{PlanName: "Otoha Max", Reference: "sub2_2"}

	summary, err := f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, summary.Plan)
	require.NotNil(t, summary.Plan.MonthlyLimitUSD)
	require.Equal(t, 120.0, *summary.Plan.MonthlyLimitUSD, "the plan's allowance, not the group's 50")
	require.NotNil(t, summary.Plan.PlanID)
	require.Equal(t, planID, *summary.Plan.PlanID)
}

func TestOtohaAccountSummaryOfAnOlderSubscriptionHasNoPlanID(t *testing.T) {
	f := newOtohaFixture(t)
	start := f.now.Add(-time.Hour)
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		StartsAt: start, ExpiresAt: f.now.Add(24 * time.Hour)}
	summary, err := f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.Nil(t, summary.Plan.PlanID)
	require.Equal(t, 50.0, *summary.Plan.MonthlyLimitUSD, "the group's limit, as before")
}
