//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The handlers' eligibility check for a group with balance fallback: a request the plan pays for is
// refused once the plan is used up, as before, so the plan is never charged past its limit (a long-lived
// connection keeps its plan); the next request is sent to the balance by the auth middleware. A request
// the balance pays for is refused only when the balance is used up too, as credit exhausted.
type fallbackEligibilityCache struct {
	billingCacheWorkerStub
	balance      float64
	monthlyUsage float64
}

func (c *fallbackEligibilityCache) GetUserBalance(context.Context, int64) (float64, error) {
	return c.balance, nil
}

func (c *fallbackEligibilityCache) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return &SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(24 * time.Hour), MonthlyUsage: c.monthlyUsage}, nil
}

func fallbackEligibility(t *testing.T, fallback bool, withPlan bool, monthlyUsage, balance float64) error {
	t.Helper()
	svc := NewBillingCacheService(&fallbackEligibilityCache{balance: balance, monthlyUsage: monthlyUsage}, nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(svc.Stop)
	limit := 20.0
	group := &Group{ID: 77, SubscriptionType: SubscriptionTypeSubscription, MonthlyLimitUSD: &limit, BalanceFallbackEnabled: fallback}
	var plan *UserSubscription
	if withPlan {
		plan = &UserSubscription{ID: 501, UserID: 9, GroupID: 77, Status: SubscriptionStatusActive}
	}
	return svc.CheckBillingEligibility(context.Background(), &User{ID: 9}, nil, group, plan, "")
}

func TestEligibilityWithBalanceFallbackNeverChargesAUsedUpPlan(t *testing.T) {
	require.NoError(t, fallbackEligibility(t, true, true, 5, 0), "the plan has credit")
	require.ErrorIs(t, fallbackEligibility(t, true, true, 25, 3), ErrMonthlyLimitExceeded, "even with a balance, the plan is not charged past its limit")
}

func TestEligibilityWithBalanceFallbackLetsTheBalancePayWithoutAPlan(t *testing.T) {
	require.NoError(t, fallbackEligibility(t, true, false, 0, 3))
	require.ErrorIs(t, fallbackEligibility(t, true, false, 0, 0), ErrCreditExhausted)
}

func TestEligibilityWithoutBalanceFallbackKeepsItsErrors(t *testing.T) {
	require.ErrorIs(t, fallbackEligibility(t, false, true, 25, 3), ErrMonthlyLimitExceeded)
	require.ErrorIs(t, fallbackEligibility(t, false, false, 0, 0), ErrInsufficientBalance)
}
