package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type searchSettlementStub struct {
	UsageBillingRepository
	cmd    *SearchSettlementCommand
	err    error
	ctxErr error
}

func (r *searchSettlementStub) SettleSearch(ctx context.Context, cmd *SearchSettlementCommand) (*SearchCostReceipt, error) {
	r.cmd, r.ctxErr = cmd, ctx.Err()
	if r.err != nil {
		return nil, r.err
	}
	return &SearchCostReceipt{AmountUSD: cmd.Quote.AmountUSD, Currency: "USD", Calls: 1, RequestID: cmd.Execution.RequestID, BillingSource: cmd.Quote.BillingSource, UnitPriceUSD: cmd.Quote.UnitPriceUSD, RateMultiplier: cmd.Quote.RateMultiplier}, nil
}

func searchTestKey() *APIKey {
	gid := int64(3)
	return &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &gid, Group: &Group{ID: gid, RateMultiplier: 1}}
}

func TestStandaloneSearchQuote(t *testing.T) {
	r := &searchSettlementStub{}
	s := &GatewayService{usageBillingRepo: r, billingService: &BillingService{}}
	k := searchTestKey()
	q, err := s.QuoteStandaloneSearch(context.Background(), k, nil)
	require.NoError(t, err)
	require.Equal(t, .01, q.AmountUSD)
	require.Equal(t, "balance", q.BillingSource)
	k.User.ID = 99
	require.Equal(t, int64(1), q.UserID)
	k = searchTestKey()
	price, rate := .000078125, 1.0
	k.Group.WebSearchPricePerCall = &price
	s.userGroupRateRepo = &userGroupRateResolverRepoStub{rate: &rate}
	q, err = s.QuoteStandaloneSearch(context.Background(), k, nil)
	require.NoError(t, err)
	require.Equal(t, .00007813, q.AmountUSD)
	price = 0
	q, err = s.QuoteStandaloneSearch(context.Background(), k, nil)
	require.NoError(t, err)
	require.Zero(t, q.AmountUSD)
	price = math.NaN()
	_, err = s.QuoteStandaloneSearch(context.Background(), k, nil)
	require.Error(t, err)
}

func TestStandaloneSearchSettlementFailClosedAndDetached(t *testing.T) {
	s := &GatewayService{billingService: &BillingService{}}
	_, err := s.QuoteStandaloneSearch(context.Background(), searchTestKey(), nil)
	require.Error(t, err)
	r := &searchSettlementStub{}
	s.usageBillingRepo = r
	q, err := s.QuoteStandaloneSearch(context.Background(), searchTestKey(), nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt, err := s.SettleStandaloneSearch(ctx, q, SearchExecution{RequestID: "search:1", Provider: "test", PayloadHash: "hash", Duration: time.Second})
	require.NoError(t, err)
	require.NoError(t, r.ctxErr)
	require.Equal(t, q.AmountUSD, receipt.AmountUSD)
	r.err = errors.New("commit failed")
	receipt, err = s.SettleStandaloneSearch(ctx, q, r.cmd.Execution)
	require.Error(t, err)
	require.Nil(t, receipt)
	_, err = s.SettleStandaloneSearch(ctx, q, SearchExecution{})
	require.Error(t, err)
}

func TestStandaloneSearchSubscriptionWindows(t *testing.T) {
	now := time.Now()
	zero := 0.0
	old := now.Add(-48 * time.Hour)
	sub := UserSubscription{Status: SubscriptionStatusActive, StartsAt: old, ExpiresAt: now.Add(48 * time.Hour), DailyWindowStart: &old, DailyUsageUSD: 10}
	require.NoError(t, PrepareSearchSubscriptionCharge(&sub, &Group{DailyLimitUSD: &zero}, .01, now), "zero subscription limit means unlimited")
	require.Equal(t, .01, sub.DailyUsageUSD)
	sub.StartsAt, sub.ExpiresAt = old, old.Add(24*time.Hour)
	require.ErrorIs(t, PrepareSearchSubscriptionCharge(&sub, &Group{}, .01, now), ErrSubscriptionExpired)
}

func TestStandaloneSearchSubscriptionQuoteAndCaps(t *testing.T) {
	now := time.Now()
	s := &GatewayService{usageBillingRepo: &searchSettlementStub{}, billingService: &BillingService{}, userGroupRateRepo: &userGroupRateResolverRepoStub{rate: float64Ptr(2.5)}}
	k := searchTestKey()
	k.Group.SubscriptionType = SubscriptionTypeSubscription
	sub := &UserSubscription{ID: 4, UserID: 1, GroupID: 3, Status: SubscriptionStatusActive, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(48 * time.Hour)}
	q, err := s.QuoteStandaloneSearch(context.Background(), k, sub)
	require.NoError(t, err)
	require.Equal(t, .025, q.AmountUSD)
	require.Equal(t, int64(4), q.SubscriptionID)
	require.Equal(t, "subscription", q.BillingSource)
	sub.UserID = 10
	_, err = s.QuoteStandaloneSearch(context.Background(), k, sub)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
	for _, tc := range []struct {
		group Group
		want  error
	}{{Group{DailyLimitUSD: float64Ptr(1)}, ErrDailyLimitExceeded}, {Group{WeeklyLimitUSD: float64Ptr(1)}, ErrWeeklyLimitExceeded}, {Group{MonthlyLimitUSD: float64Ptr(1)}, ErrMonthlyLimitExceeded}} {
		copy := *sub
		copy.DailyWindowStart, copy.WeeklyWindowStart, copy.MonthlyWindowStart = &now, &now, &now
		copy.DailyUsageUSD, copy.WeeklyUsageUSD, copy.MonthlyUsageUSD = 1, 1, 1
		require.ErrorIs(t, PrepareSearchSubscriptionCharge(&copy, &tc.group, .01, now), tc.want)
	}
	// A one-day subscription must not receive another daily allowance at midnight.
	start := now.Add(-23 * time.Hour)
	oneDay := UserSubscription{Status: SubscriptionStatusActive, StartsAt: start, ExpiresAt: start.Add(24 * time.Hour), DailyWindowStart: &start, DailyUsageUSD: 1}
	require.ErrorIs(t, PrepareSearchSubscriptionCharge(&oneDay, &Group{DailyLimitUSD: float64Ptr(1)}, .01, now), ErrDailyLimitExceeded)
}

type searchCacheStub struct {
	BillingCache
	ownerCalls, keyCalls int
	err                  error
}

func (c *searchCacheStub) InvalidateUserBalance(context.Context, int64) error {
	c.ownerCalls++
	return c.err
}
func (c *searchCacheStub) InvalidateSubscriptionCache(context.Context, int64, int64) error {
	c.ownerCalls++
	return c.err
}
func (c *searchCacheStub) InvalidateAPIKeyRateLimit(context.Context, int64) error {
	c.keyCalls++
	return c.err
}

func TestStandaloneSearchCacheFailureKeepsCommittedReceipt(t *testing.T) {
	cache := &searchCacheStub{err: errors.New("redis unavailable")}
	s := &GatewayService{usageBillingRepo: &searchSettlementStub{}, billingService: &BillingService{}, billingCacheService: &BillingCacheService{cache: cache}}
	q, err := s.QuoteStandaloneSearch(context.Background(), searchTestKey(), nil)
	require.NoError(t, err)
	r, err := s.SettleStandaloneSearch(context.Background(), q, SearchExecution{RequestID: "search:1", Provider: "test", PayloadHash: "hash"})
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, 1, cache.ownerCalls)
	require.Equal(t, 1, cache.keyCalls)
}
