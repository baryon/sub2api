//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TASK-57: a subscription bought as a plan carries the plan's own daily, weekly and monthly allowance. Each
// window uses the subscription's allowance when it has one and the group's otherwise, so subscriptions
// without an allowance of their own behave exactly as before.

func allowanceFloat(v float64) *float64 { return &v }

func allowanceGroup() *Group {
	return &Group{
		ID:               77,
		SubscriptionType: SubscriptionTypeSubscription,
		DailyLimitUSD:    allowanceFloat(5),
		WeeklyLimitUSD:   allowanceFloat(30),
		MonthlyLimitUSD:  allowanceFloat(100),
	}
}

func TestEffectiveLimitsWithoutOwnAllowanceAreTheGroups(t *testing.T) {
	group := allowanceGroup()
	sub := &UserSubscription{}

	limits := sub.EffectiveLimits(group)
	require.Same(t, group.DailyLimitUSD, limits.DailyUSD)
	require.Same(t, group.WeeklyLimitUSD, limits.WeeklyUSD)
	require.Same(t, group.MonthlyLimitUSD, limits.MonthlyUSD)

	zero := 0.0
	unlimited := &Group{ID: 1, MonthlyLimitUSD: &zero}
	limits = sub.EffectiveLimits(unlimited)
	require.Same(t, unlimited.MonthlyLimitUSD, limits.MonthlyUSD, "the group's value is passed through as it is")
	require.False(t, limits.HasMonthly(), "zero means no limit, as for groups")
	require.False(t, limits.HasDaily())
	require.Nil(t, limits.DailyUSD)
}

func TestEffectiveLimitsUseTheSubscriptionsOwnAllowancePerWindow(t *testing.T) {
	group := allowanceGroup()
	sub := &UserSubscription{MonthlyLimitUSD: allowanceFloat(40), WeeklyLimitUSD: allowanceFloat(0)}

	limits := sub.EffectiveLimits(group)
	require.InDelta(t, 40, *limits.MonthlyUSD, 1e-9)
	require.True(t, limits.HasMonthly())
	require.Same(t, group.DailyLimitUSD, limits.DailyUSD, "no daily allowance of its own: the group's")
	require.Same(t, group.WeeklyLimitUSD, limits.WeeklyUSD, "a zero allowance counts as not set")

	require.Nil(t, (&UserSubscription{}).EffectiveLimits(nil).MonthlyUSD, "no group, no own allowance: unlimited")
	require.InDelta(t, 40, *sub.EffectiveLimits(nil).MonthlyUSD, 1e-9)
}

func TestSubscriptionLimitChecksUseTheOwnAllowance(t *testing.T) {
	group := allowanceGroup()
	own := &UserSubscription{MonthlyUsageUSD: 45, MonthlyLimitUSD: allowanceFloat(40)}
	legacy := &UserSubscription{MonthlyUsageUSD: 45}

	require.False(t, own.CheckMonthlyLimit(group, 0))
	require.True(t, legacy.CheckMonthlyLimit(group, 0), "the group allows 100")
	require.True(t, (&UserSubscription{MonthlyUsageUSD: 45, MonthlyLimitUSD: allowanceFloat(200)}).CheckMonthlyLimit(group, 0),
		"a larger plan allowance beats the group's")
}

func TestValidateAndCheckLimitsUsesTheOwnAllowance(t *testing.T) {
	now := time.Now()
	svc := NewSubscriptionService(nil, nil, nil, nil, nil)
	svc.now = func() time.Time { return now }
	start := now.Add(-time.Hour)
	base := UserSubscription{
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          now.Add(29 * 24 * time.Hour),
		DailyWindowStart:   &start,
		WeeklyWindowStart:  &start,
		MonthlyWindowStart: &start,
		MonthlyUsageUSD:    45,
	}

	own := base
	own.MonthlyLimitUSD = allowanceFloat(40)
	_, err := svc.ValidateAndCheckLimits(&own, allowanceGroup())
	require.ErrorIs(t, err, ErrMonthlyLimitExceeded)

	legacy := base
	_, err = svc.ValidateAndCheckLimits(&legacy, allowanceGroup())
	require.NoError(t, err, "without its own allowance the group's 100 applies, as before")
}

type allowanceEligibilityCache struct {
	billingCacheWorkerStub
	monthlyUsage float64
}

func (c *allowanceEligibilityCache) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return &SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(24 * time.Hour), MonthlyUsage: c.monthlyUsage}, nil
}

func allowanceEligibility(t *testing.T, sub *UserSubscription, monthlyUsage float64) error {
	t.Helper()
	svc := NewBillingCacheService(&allowanceEligibilityCache{monthlyUsage: monthlyUsage}, nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(svc.Stop)
	return svc.CheckBillingEligibility(context.Background(), &User{ID: 9}, nil, allowanceGroup(), sub, "")
}

func TestBillingEligibilityUsesTheSubscriptionsOwnAllowance(t *testing.T) {
	plus := &UserSubscription{ID: 1, UserID: 9, GroupID: 77, Status: SubscriptionStatusActive, MonthlyLimitUSD: allowanceFloat(40)}
	require.NoError(t, allowanceEligibility(t, plus, 39))
	require.ErrorIs(t, allowanceEligibility(t, plus, 40), ErrMonthlyLimitExceeded)

	max := &UserSubscription{ID: 2, UserID: 9, GroupID: 77, Status: SubscriptionStatusActive, MonthlyLimitUSD: allowanceFloat(400)}
	require.NoError(t, allowanceEligibility(t, max, 150), "Max's allowance is larger than the group's 100")

	legacy := &UserSubscription{ID: 3, UserID: 9, GroupID: 77, Status: SubscriptionStatusActive}
	require.NoError(t, allowanceEligibility(t, legacy, 99))
	require.ErrorIs(t, allowanceEligibility(t, legacy, 100), ErrMonthlyLimitExceeded, "the group's limit, as before")
}

func TestSubscriptionProgressShowsTheOwnAllowance(t *testing.T) {
	svc := NewSubscriptionService(nil, nil, nil, nil, nil)
	start := time.Now().Add(-time.Hour)
	sub := &UserSubscription{
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 30),
		MonthlyWindowStart: &start,
		DailyWindowStart:   &start,
		MonthlyUsageUSD:    10,
		MonthlyLimitUSD:    allowanceFloat(40),
	}

	progress := svc.calculateProgress(sub, allowanceGroup())
	require.NotNil(t, progress.Monthly)
	require.InDelta(t, 40, progress.Monthly.LimitUSD, 1e-9)
	require.InDelta(t, 30, progress.Monthly.RemainingUSD, 1e-9)
	require.NotNil(t, progress.Daily)
	require.InDelta(t, 5, progress.Daily.LimitUSD, 1e-9, "the daily window keeps the group's limit")

	legacy := *sub
	legacy.MonthlyLimitUSD = nil
	require.InDelta(t, 100, svc.calculateProgress(&legacy, allowanceGroup()).Monthly.LimitUSD, 1e-9)
}

// The upgrade credit: the old plan's unused allowance in the current 30-day window, in proportion to the time
// left in that window, plus the full allowance of any later window the user already paid for (an earlier
// renewal), in proportion to its length.
func TestPlanUpgradeCreditProratesTheUnusedAllowance(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := &UserSubscription{
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 30),
		MonthlyWindowStart: &start,
		MonthlyUsageUSD:    20,
		MonthlyLimitUSD:    allowanceFloat(100),
	}

	// Half the window is left, 80 unused: 40.
	require.InDelta(t, 40, planUpgradeCredit(sub, allowanceGroup(), start.Add(15*24*time.Hour)), 1e-6)
	// Nothing used, bought just now: the whole allowance.
	fresh := *sub
	fresh.MonthlyUsageUSD = 0
	require.InDelta(t, 100, planUpgradeCredit(&fresh, allowanceGroup(), start), 1e-6)
	// Allowance used up: nothing.
	usedUp := *sub
	usedUp.MonthlyUsageUSD = 130
	require.Zero(t, planUpgradeCredit(&usedUp, allowanceGroup(), start.Add(24*time.Hour)))
	// Expired: nothing.
	require.Zero(t, planUpgradeCredit(sub, allowanceGroup(), start.AddDate(0, 0, 31)))
}

func TestPlanUpgradeCreditCountsPrepaidLaterWindows(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := &UserSubscription{
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 60), // renewed once before the upgrade
		MonthlyWindowStart: &start,
		MonthlyUsageUSD:    50,
		MonthlyLimitUSD:    allowanceFloat(100),
	}
	// 10 of 30 days left in this window with 50 unused (16.67), plus a whole prepaid window (100).
	require.InDelta(t, 50.0/3+100, planUpgradeCredit(sub, allowanceGroup(), start.Add(20*24*time.Hour)), 0.01)
}

func TestPlanUpgradeCreditStartsAFreshWindowWhenTheOldOneHasRolledOver(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := &UserSubscription{
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 60),
		MonthlyWindowStart: &start, // not yet advanced: the first request of the new window resets it
		MonthlyUsageUSD:    90,
		MonthlyLimitUSD:    allowanceFloat(100),
	}
	// Day 45: the second window [30, 60) is half gone and nothing of it has been used.
	require.InDelta(t, 50, planUpgradeCredit(sub, allowanceGroup(), start.Add(45*24*time.Hour)), 1e-6)

	unused := *sub
	unused.MonthlyWindowStart = nil
	unused.MonthlyUsageUSD = 0
	// Never used: the windows count from the start of the term.
	require.InDelta(t, 150, planUpgradeCredit(&unused, allowanceGroup(), start.Add(15*24*time.Hour)), 1e-6)
}

func TestPlanUpgradeCreditUsesTheGroupsLimitForOlderSubscriptionsAndNothingWhenUnlimited(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	legacy := &UserSubscription{
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 30),
		MonthlyWindowStart: &start,
	}
	require.InDelta(t, 50, planUpgradeCredit(legacy, allowanceGroup(), start.Add(15*24*time.Hour)), 1e-6)
	require.Zero(t, planUpgradeCredit(legacy, &Group{ID: 1}, start.Add(15*24*time.Hour)), "an unlimited plan has no allowance to credit")
}

func TestPlanUpgradeCreditIsRoundedDownToCents(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := &UserSubscription{
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 30),
		MonthlyWindowStart: &start,
		MonthlyLimitUSD:    allowanceFloat(100),
	}
	credit := planUpgradeCredit(sub, nil, start.Add(10*24*time.Hour)) // 66.666...
	require.InDelta(t, 66.66, credit, 1e-9)
}

func TestClassifyPlanPurchase(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	plusID, proID := int64(1), int64(2)
	active := func(planID *int64) *UserSubscription {
		return &UserSubscription{Status: SubscriptionStatusActive, StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.AddDate(0, 0, 20), PlanID: planID}
	}
	price := func(v float64) *float64 { return &v }

	require.Equal(t, planPurchaseNew, classifyPlanPurchase(nil, now, proID, 50, nil))
	require.Equal(t, planPurchaseRenew, classifyPlanPurchase(active(&proID), now, proID, 50, price(50)), "same plan")
	require.Equal(t, planPurchaseRenew, classifyPlanPurchase(active(nil), now, proID, 50, nil), "a subscription from before plans had allowances")
	require.Equal(t, planPurchaseUpgrade, classifyPlanPurchase(active(&plusID), now, proID, 50, price(20)))
	require.Equal(t, planPurchaseUpgrade, classifyPlanPurchase(active(&plusID), now, proID, 50, price(50)), "same price, another plan: switch now")
	require.Equal(t, planPurchaseUpgrade, classifyPlanPurchase(active(&plusID), now, proID, 50, nil), "the old plan was deleted: switch now")
	require.Equal(t, planPurchaseDowngrade, classifyPlanPurchase(active(&proID), now, plusID, 20, price(50)))

	expired := active(&proID)
	expired.ExpiresAt = now.Add(-time.Minute)
	require.Equal(t, planPurchaseRenew, classifyPlanPurchase(expired, now, plusID, 20, price(50)), "an ended plan: any plan starts a new term")
	expiredStatus := active(&proID)
	expiredStatus.Status = SubscriptionStatusExpired
	require.Equal(t, planPurchaseRenew, classifyPlanPurchase(expiredStatus, now, plusID, 20, price(50)))
}
