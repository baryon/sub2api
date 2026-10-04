//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-57 against Postgres with the real repositories: paying for a dearer plan switches the subscription to it
// at once and credits the old plan's unused allowance to the balance exactly once, however many times the
// payment notification arrives, also concurrently.
type planUpgradeFixture struct {
	ctx     context.Context
	client  *dbent.Client
	payment *service.PaymentService
	subRepo service.UserSubscriptionRepository
	user    *service.User
	group   *service.Group
	plus    *dbent.SubscriptionPlan
	pro     *dbent.SubscriptionPlan
}

func newPlanUpgradeFixture(t *testing.T) *planUpgradeFixture {
	t.Helper()
	ctx := context.Background()
	client := integrationEntClient
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, client, &service.Group{
		Name: fmt.Sprintf("plan-upgrade-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("plan-upgrade-%d@example.com", suffix), Balance: 3})

	newPlan := func(name string, price, monthly float64) *dbent.SubscriptionPlan {
		p, err := client.SubscriptionPlan.Create().SetGroupID(group.ID).SetName(name).SetPrice(price).
			SetValidityDays(30).SetValidityUnit("day").SetForSale(true).SetMonthlyLimitUsd(monthly).Save(ctx)
		require.NoError(t, err)
		return p
	}
	plus := newPlan("Plus", 20, 40)
	pro := newPlan("Pro", 50, 120)

	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM payment_audit_logs WHERE order_id IN (SELECT id::text FROM payment_orders WHERE user_id = $1)",
			"DELETE FROM payment_orders WHERE user_id = $1",
			"DELETE FROM redeem_codes WHERE used_by = $1",
			"DELETE FROM user_subscriptions WHERE user_id = $1",
			"DELETE FROM users WHERE id = $1",
		} {
			_, err := integrationDB.ExecContext(ctx, q, user.ID)
			require.NoError(t, err, q)
		}
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM subscription_plans WHERE group_id = $1", group.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
		require.NoError(t, err)
	})

	groupRepo := NewGroupRepository(client, integrationDB)
	subRepo := NewUserSubscriptionRepository(client)
	subscriptionSvc := service.NewSubscriptionService(groupRepo, subRepo, nil, client, nil)
	configSvc := service.NewPaymentConfigService(client, nil, nil)
	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, subscriptionSvc, configSvc,
		NewUserRepository(client, integrationDB), groupRepo, nil)
	return &planUpgradeFixture{ctx: ctx, client: client, payment: paymentSvc, subRepo: subRepo, user: user, group: group, plus: plus, pro: pro}
}

func (f *planUpgradeFixture) seedPlus(t *testing.T, startedAgo time.Duration, monthlyUsage float64) {
	t.Helper()
	start := time.Now().Add(-startedAgo)
	planID := f.plus.ID
	monthly := 40.0
	require.NoError(t, f.subRepo.Create(f.ctx, &service.UserSubscription{
		UserID: f.user.ID, GroupID: f.group.ID, StartsAt: start, ExpiresAt: start.AddDate(0, 0, 30),
		Status: service.SubscriptionStatusActive, AssignedAt: start,
		DailyWindowStart: &start, WeeklyWindowStart: &start, MonthlyWindowStart: &start,
		MonthlyUsageUSD: monthlyUsage, PlanID: &planID, MonthlyLimitUSD: &monthly,
	}))
}

func (f *planUpgradeFixture) order(t *testing.T, plan *dbent.SubscriptionPlan) *dbent.PaymentOrder {
	t.Helper()
	n := time.Now().UnixNano()
	o, err := f.client.PaymentOrder.Create().
		SetUserID(f.user.ID).SetUserEmail(f.user.Email).SetUserName("buyer").
		SetAmount(plan.Price).SetPayAmount(plan.Price).SetFeeRate(0).
		SetRechargeCode(fmt.Sprintf("PAY-UPG-%d", n)).SetOutTradeNo(fmt.Sprintf("sub2_upg_%d", n)).
		SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo(fmt.Sprintf("trade-%d", n)).
		SetOrderType(payment.OrderTypeSubscription).SetPlanID(plan.ID).
		SetSubscriptionGroupID(f.group.ID).SetSubscriptionDays(30).
		SetStatus(service.OrderStatusPaid).SetPaidAt(time.Now()).SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").SetSrcHost("api.example.com").
		Save(f.ctx)
	require.NoError(t, err)
	return o
}

func (f *planUpgradeFixture) balance(t *testing.T) float64 {
	t.Helper()
	u, err := f.client.User.Get(f.ctx, f.user.ID)
	require.NoError(t, err)
	return u.Balance
}

func (f *planUpgradeFixture) credits(t *testing.T) []*dbent.RedeemCode {
	t.Helper()
	records, err := f.client.RedeemCode.Query().Where(redeemcode.UsedByEQ(f.user.ID), redeemcode.TypeEQ(service.RedeemTypePlanCredit)).All(f.ctx)
	require.NoError(t, err)
	return records
}

func TestPlanUpgradeFulfillmentInPostgres(t *testing.T) {
	f := newPlanUpgradeFixture(t)
	// Plus (40 a month) from 15 days ago with 10 used: 30 unused, half the window left → 15.
	f.seedPlus(t, 15*24*time.Hour, 10)
	o := f.order(t, f.pro)

	require.NoError(t, f.payment.ExecuteSubscriptionFulfillment(f.ctx, o.ID))

	sub, err := f.subRepo.GetActiveByUserIDAndGroupID(f.ctx, f.user.ID, f.group.ID)
	require.NoError(t, err)
	require.Equal(t, f.pro.ID, *sub.PlanID)
	require.InDelta(t, 120, *sub.MonthlyLimitUSD, 1e-9)
	require.Zero(t, sub.MonthlyUsageUSD)
	require.WithinDuration(t, time.Now().AddDate(0, 0, 30), sub.ExpiresAt, time.Minute)
	require.InDelta(t, 18, f.balance(t), 0.011)
	records := f.credits(t)
	require.Len(t, records, 1)
	require.InDelta(t, 15, records[0].Value, 0.011)

	// The provider sends the notification again after a lost lease: nothing more is credited.
	staleAt := time.Now().Add(-time.Hour)
	_, err = f.client.PaymentOrder.UpdateOneID(o.ID).SetStatus(service.OrderStatusRecharging).SetUpdatedAt(staleAt).ClearCompletedAt().Save(f.ctx)
	require.NoError(t, err)
	require.NoError(t, f.payment.ExecuteSubscriptionFulfillment(f.ctx, o.ID))
	require.InDelta(t, 18, f.balance(t), 0.011)
	require.Len(t, f.credits(t), 1)
}

func TestPlanUpgradeFulfillmentUnderConcurrentNotifications(t *testing.T) {
	f := newPlanUpgradeFixture(t)
	f.seedPlus(t, 15*24*time.Hour, 10)
	o := f.order(t, f.pro)

	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = f.payment.ExecuteSubscriptionFulfillment(f.ctx, o.ID)
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	require.GreaterOrEqual(t, succeeded, 1, "errors: %v", errs)
	completed, err := f.client.PaymentOrder.Get(f.ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, completed.Status)
	require.InDelta(t, 18, f.balance(t), 0.011, "credited once")
	require.Len(t, f.credits(t), 1)
}
