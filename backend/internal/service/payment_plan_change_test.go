//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// TASK-57: buying a plan records the plan and its allowance on the subscription. The same plan extends the
// term; a dearer plan starts now with a fresh term and credits the old plan's unused allowance to the balance;
// a cheaper plan is not available while the current one runs. Everything stays idempotent under webhook
// retries.

const planChangeGroupID = int64(7)

type planChangeFixture struct {
	t        *testing.T
	ctx      context.Context
	client   *dbent.Client
	subRepo  *subscriptionUserSubRepoStub
	svc      *PaymentService
	userID   int64
	plus     *dbent.SubscriptionPlan
	pro      *dbent.SubscriptionPlan
	plain    *dbent.SubscriptionPlan
	groupLim float64
}

func newPlanChangeFixture(t *testing.T) *planChangeFixture {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user, err := client.User.Create().
		SetEmail("plan-change-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com").
		SetPasswordHash("hash").
		SetUsername("plan-change-user").
		SetBalance(3).
		Save(ctx)
	require.NoError(t, err)

	newPlan := func(name string, price float64, monthly *float64) *dbent.SubscriptionPlan {
		b := client.SubscriptionPlan.Create().SetGroupID(planChangeGroupID).SetName(name).SetPrice(price).
			SetValidityDays(30).SetValidityUnit("day").SetForSale(true)
		if monthly != nil {
			b.SetMonthlyLimitUsd(*monthly)
		}
		p, err := b.Save(ctx)
		require.NoError(t, err)
		return p
	}

	f := &planChangeFixture{t: t, ctx: ctx, client: client, userID: user.ID, groupLim: 100}
	f.plus = newPlan("Plus", 20, allowanceFloat(40))
	f.pro = newPlan("Pro", 50, allowanceFloat(120))
	f.plain = newPlan("Plain", 10, nil)

	f.subRepo = newSubscriptionUserSubRepoStub()
	groupRepo := &subscriptionGroupRepoStub{group: &Group{
		ID:               planChangeGroupID,
		Status:           payment.EntityStatusActive,
		SubscriptionType: SubscriptionTypeSubscription,
		MonthlyLimitUSD:  &f.groupLim,
	}}
	f.svc = &PaymentService{
		entClient:       client,
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, f.subRepo, nil, nil, nil),
		configService:   &PaymentConfigService{entClient: client},
	}
	return f
}

func (f *planChangeFixture) order(plan *dbent.SubscriptionPlan) *dbent.PaymentOrder {
	f.t.Helper()
	n := strconv.FormatInt(time.Now().UnixNano(), 10)
	o, err := f.client.PaymentOrder.Create().
		SetUserID(f.userID).
		SetUserEmail("buyer@example.com").
		SetUserName("buyer").
		SetAmount(plan.Price).
		SetPayAmount(plan.Price).
		SetFeeRate(0).
		SetRechargeCode("PAY-PLAN-" + n).
		SetOutTradeNo("sub2_plan_" + n).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-" + n).
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(plan.ID).
		SetSubscriptionGroupID(planChangeGroupID).
		SetSubscriptionDays(30).
		SetStatus(OrderStatusPaid).
		SetPaidAt(time.Now()).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(f.ctx)
	require.NoError(f.t, err)
	return o
}

func (f *planChangeFixture) subscription() *UserSubscription {
	f.t.Helper()
	sub, err := f.subRepo.GetByUserIDAndGroupID(f.ctx, f.userID, planChangeGroupID)
	require.NoError(f.t, err)
	return sub
}

func (f *planChangeFixture) balance() float64 {
	f.t.Helper()
	u, err := f.client.User.Get(f.ctx, f.userID)
	require.NoError(f.t, err)
	return u.Balance
}

func (f *planChangeFixture) seed(plan *dbent.SubscriptionPlan, startedAgo time.Duration, monthlyUsage float64) *UserSubscription {
	start := time.Now().Add(-startedAgo)
	sub := &UserSubscription{
		ID:                 500,
		UserID:             f.userID,
		GroupID:            planChangeGroupID,
		StartsAt:           start,
		ExpiresAt:          start.AddDate(0, 0, 30),
		Status:             SubscriptionStatusActive,
		DailyWindowStart:   &start,
		WeeklyWindowStart:  &start,
		MonthlyWindowStart: &start,
		MonthlyUsageUSD:    monthlyUsage,
	}
	if plan != nil {
		(&subscriptionPlanTerms{PlanID: plan.ID, Limits: SubscriptionLimits{MonthlyUSD: plan.MonthlyLimitUsd}}).apply(sub)
	}
	f.subRepo.seed(sub)
	return sub
}

func (f *planChangeFixture) creditRecords(orderID int64) []*dbent.RedeemCode {
	f.t.Helper()
	records, err := f.client.RedeemCode.Query().Where(redeemcode.UsedByEQ(f.userID), redeemcode.TypeEQ(RedeemTypePlanCredit)).All(f.ctx)
	require.NoError(f.t, err)
	return records
}

func (f *planChangeFixture) replay(o *dbent.PaymentOrder) {
	f.t.Helper()
	staleAt := time.Now().Add(-paymentFulfillmentLeaseDuration - time.Minute)
	_, err := f.client.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRecharging).SetUpdatedAt(staleAt).ClearCompletedAt().Save(f.ctx)
	require.NoError(f.t, err)
	require.NoError(f.t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))
}

func TestPlanPurchaseRecordsThePlansAllowanceOnANewSubscription(t *testing.T) {
	f := newPlanChangeFixture(t)
	o := f.order(f.pro)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))

	sub := f.subscription()
	require.NotNil(t, sub.PlanID)
	require.Equal(t, f.pro.ID, *sub.PlanID)
	require.NotNil(t, sub.MonthlyLimitUSD)
	require.InDelta(t, 120, *sub.MonthlyLimitUSD, 1e-9)
	require.Nil(t, sub.DailyLimitUSD, "the plan sets no daily allowance: the group's applies")
	require.WithinDuration(t, time.Now().AddDate(0, 0, 30), sub.ExpiresAt, time.Minute)
	require.InDelta(t, 3, f.balance(), 1e-9)
}

func TestPlanPurchaseOfTheSamePlanExtendsTheTerm(t *testing.T) {
	f := newPlanChangeFixture(t)
	seeded := f.seed(f.pro, 20*24*time.Hour, 60)
	// The admin raised Pro's allowance since; the renewal records the plan as it is now.
	_, err := f.client.SubscriptionPlan.UpdateOneID(f.pro.ID).SetMonthlyLimitUsd(150).Save(f.ctx)
	require.NoError(t, err)

	o := f.order(f.pro)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))

	sub := f.subscription()
	require.True(t, sub.ExpiresAt.Equal(seeded.ExpiresAt.AddDate(0, 0, 30)), "extended by 30 days from the current end")
	require.True(t, sub.StartsAt.Equal(seeded.StartsAt), "the term is not restarted")
	require.InDelta(t, 60, sub.MonthlyUsageUSD, 1e-9, "this period's use is kept")
	require.InDelta(t, 150, *sub.MonthlyLimitUSD, 1e-9)
	require.InDelta(t, 3, f.balance(), 1e-9, "no credit for a renewal")
	require.Empty(t, f.creditRecords(o.ID))
}

func TestPlanPurchaseOnAnOlderSubscriptionExtendsItAsBefore(t *testing.T) {
	f := newPlanChangeFixture(t)
	seeded := f.seed(nil, 5*24*time.Hour, 10)

	o := f.order(f.plain)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))

	sub := f.subscription()
	require.True(t, sub.ExpiresAt.Equal(seeded.ExpiresAt.AddDate(0, 0, 30)))
	require.InDelta(t, 10, sub.MonthlyUsageUSD, 1e-9)
	require.Equal(t, f.plain.ID, *sub.PlanID, "the plan is recorded for the next purchase")
	require.Nil(t, sub.MonthlyLimitUSD, "a plan without an allowance keeps the group's limit")
	require.InDelta(t, 3, f.balance(), 1e-9)
}

func TestPlanPurchaseWhosePlanWasDeletedKeepsTheOldBehaviour(t *testing.T) {
	f := newPlanChangeFixture(t)
	seeded := f.seed(nil, 5*24*time.Hour, 10)
	o := f.order(f.plain)
	require.NoError(t, f.client.SubscriptionPlan.DeleteOneID(f.plain.ID).Exec(f.ctx))

	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))
	sub := f.subscription()
	require.True(t, sub.ExpiresAt.Equal(seeded.ExpiresAt.AddDate(0, 0, 30)))
	require.Nil(t, sub.PlanID)
	require.Nil(t, sub.MonthlyLimitUSD)
}

func TestPlanUpgradeStartsNowAndCreditsTheUnusedAllowanceOnce(t *testing.T) {
	f := newPlanChangeFixture(t)
	// Plus ($20, 40 a month) bought 15 days ago, 10 used: 30 unused × half the window = 15, capped at what half
	// of Plus cost ($10).
	f.seed(f.plus, 15*24*time.Hour, 10)

	o := f.order(f.pro)
	before := time.Now()
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))

	sub := f.subscription()
	require.Equal(t, f.pro.ID, *sub.PlanID)
	require.InDelta(t, 120, *sub.MonthlyLimitUSD, 1e-9, "the new allowance applies at once")
	require.False(t, sub.StartsAt.Before(before), "a fresh term from the purchase")
	require.WithinDuration(t, sub.StartsAt.AddDate(0, 0, 30), sub.ExpiresAt, time.Second)
	require.Zero(t, sub.MonthlyUsageUSD, "the new period starts unused")
	require.Equal(t, SubscriptionStatusActive, sub.Status)

	require.InDelta(t, 3+10, f.balance(), 0.02)
	records := f.creditRecords(o.ID)
	require.Len(t, records, 1)
	require.InDelta(t, 10, records[0].Value, 0.02)
	require.Equal(t, StatusUsed, records[0].Status)
	require.NotNil(t, records[0].UsedAt)
	require.NotNil(t, records[0].Notes)
	require.Contains(t, *records[0].Notes, "Plus")
	require.Contains(t, *records[0].Notes, "Pro")

	audit, err := f.client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("SUBSCRIPTION_ASSIGNED")).
		Only(f.ctx)
	require.NoError(t, err)
	var detail struct {
		PlanChange    string  `json:"planChange"`
		UpgradeCredit float64 `json:"upgradeCredit"`
	}
	require.NoError(t, json.Unmarshal([]byte(audit.Detail), &detail))
	require.Equal(t, "upgrade", detail.PlanChange)
	require.InDelta(t, 10, detail.UpgradeCredit, 0.02)

	completed, err := f.client.PaymentOrder.Get(f.ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, completed.Status)

	// A webhook retry or a recovery after a lost lease changes nothing.
	f.replay(o)
	require.InDelta(t, 3+10, f.balance(), 0.02)
	require.Len(t, f.creditRecords(o.ID), 1)
	again := f.subscription()
	require.True(t, again.ExpiresAt.Equal(sub.ExpiresAt))
}

func TestPlanPurchaseAfterAPlanWithoutAllowanceRenewsAsBefore(t *testing.T) {
	f := newPlanChangeFixture(t)
	seeded := f.seed(f.plain, 2*24*time.Hour, 3)

	o := f.order(f.pro)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))
	sub := f.subscription()
	require.True(t, sub.ExpiresAt.Equal(seeded.ExpiresAt.AddDate(0, 0, 30)), "not a tier change: extended")
	require.InDelta(t, 3, sub.MonthlyUsageUSD, 1e-9)
	require.Equal(t, f.pro.ID, *sub.PlanID, "from now on the subscription is on Pro")
	require.InDelta(t, 120, *sub.MonthlyLimitUSD, 1e-9)
	require.InDelta(t, 3, f.balance(), 1e-9)
	require.Empty(t, f.creditRecords(o.ID))
}

// A group selling several plans without allowances (say monthly and quarterly) keeps the old rule: any of its
// plans extends the subscription.
func TestPlanPurchaseOnAGroupWithoutTieredPlansExtendsAsBefore(t *testing.T) {
	f := newPlanChangeFixture(t)
	quarterly, err := f.client.SubscriptionPlan.Create().SetGroupID(planChangeGroupID).SetName("Quarterly").SetPrice(27).
		SetValidityDays(90).SetValidityUnit("day").SetForSale(true).Save(f.ctx)
	require.NoError(t, err)
	seeded := f.seed(f.plain, 10*24*time.Hour, 3)

	_, err = f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: quarterly.ID})
	require.NoError(t, err)
	o := f.order(quarterly)
	_, err = f.client.PaymentOrder.UpdateOneID(o.ID).SetSubscriptionDays(90).Save(f.ctx)
	require.NoError(t, err)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))

	sub := f.subscription()
	require.True(t, sub.ExpiresAt.Equal(seeded.ExpiresAt.AddDate(0, 0, 90)))
	require.True(t, sub.StartsAt.Equal(seeded.StartsAt))
	require.Nil(t, sub.MonthlyLimitUSD)
	require.InDelta(t, 3, f.balance(), 1e-9)

	// And the cheaper monthly plan afterwards is a renewal too, not a refused downgrade.
	_, err = f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: f.plain.ID})
	require.NoError(t, err)
}

func TestPlanUpgradeOfASuspendedSubscriptionCreditsNothing(t *testing.T) {
	f := newPlanChangeFixture(t)
	seeded := f.seed(f.plus, 5*24*time.Hour, 0)
	seeded.Status = SubscriptionStatusSuspended
	f.subRepo.seed(seeded)

	o := f.order(f.pro)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))
	sub := f.subscription()
	require.Equal(t, f.pro.ID, *sub.PlanID)
	require.InDelta(t, 3, f.balance(), 1e-9, "a suspended plan is not turned into balance")
	require.Empty(t, f.creditRecords(o.ID))
}

func TestPlanDowngradeDuringThePeriodFailsTheOrderAndChangesNothing(t *testing.T) {
	f := newPlanChangeFixture(t)
	seeded := f.seed(f.pro, 5*24*time.Hour, 10)

	o := f.order(f.plus)
	err := f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID)
	require.Error(t, err)
	require.Equal(t, "PLAN_DOWNGRADE_NOT_ALLOWED", infraerrors.Reason(err))

	sub := f.subscription()
	require.Equal(t, f.pro.ID, *sub.PlanID)
	require.True(t, sub.ExpiresAt.Equal(seeded.ExpiresAt))
	require.InDelta(t, 3, f.balance(), 1e-9)

	failed, err := f.client.PaymentOrder.Get(f.ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusFailed, failed.Status, "left for the admin to refund, or to retry once Pro has ended")
	require.NotNil(t, failed.FailedReason)
	require.Contains(t, *failed.FailedReason, "once the current plan ends")
}

func TestPlanPurchaseAfterThePlanEndedStartsTheNewPlan(t *testing.T) {
	f := newPlanChangeFixture(t)
	f.seed(f.pro, 40*24*time.Hour, 90) // ended 10 days ago

	o := f.order(f.plus)
	require.NoError(t, f.svc.ExecuteSubscriptionFulfillment(f.ctx, o.ID))
	sub := f.subscription()
	require.Equal(t, f.plus.ID, *sub.PlanID)
	require.InDelta(t, 40, *sub.MonthlyLimitUSD, 1e-9)
	require.WithinDuration(t, time.Now().AddDate(0, 0, 30), sub.ExpiresAt, time.Minute)
	require.Zero(t, sub.MonthlyUsageUSD)
	require.InDelta(t, 3, f.balance(), 1e-9, "nothing is left of an ended plan")
}

// The order is refused before payment when it would be a downgrade, so the user is not charged for a plan that
// cannot start yet.
func (s *subscriptionUserSubRepoStub) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	sub, err := s.GetByUserIDAndGroupID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	if sub.Status != SubscriptionStatusActive || !sub.ExpiresAt.After(time.Now()) {
		return nil, ErrSubscriptionNotFound
	}
	return sub, nil
}

func TestPlanOrderIsRefusedWhenItWouldBeADowngrade(t *testing.T) {
	f := newPlanChangeFixture(t)
	f.seed(f.pro, 5*24*time.Hour, 10)

	_, err := f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: f.plus.ID})
	require.Error(t, err)
	require.Equal(t, "PLAN_DOWNGRADE_NOT_ALLOWED", infraerrors.Reason(err))
	app := infraerrors.FromError(err)
	require.Equal(t, "Pro", app.Metadata["current_plan"])
	require.NotEmpty(t, app.Metadata["available_at"])

	_, err = f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: f.pro.ID})
	require.NoError(t, err, "renewing the same plan")
}

func TestPlanOrderAtTheSamePriceOrOnASuspendedPlanIsRefusedToo(t *testing.T) {
	f := newPlanChangeFixture(t)
	twin, err := f.client.SubscriptionPlan.Create().SetGroupID(planChangeGroupID).SetName("Pro Twin").SetPrice(50).
		SetValidityDays(30).SetValidityUnit("day").SetForSale(true).SetMonthlyLimitUsd(130).Save(f.ctx)
	require.NoError(t, err)
	seeded := f.seed(f.pro, 5*24*time.Hour, 10)

	_, err = f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: twin.ID})
	require.Equal(t, "PLAN_DOWNGRADE_NOT_ALLOWED", infraerrors.Reason(err), "same price: after the current plan ends")

	seeded.Status = SubscriptionStatusSuspended
	f.subRepo.seed(seeded)
	_, err = f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: f.plus.ID})
	require.Equal(t, "PLAN_DOWNGRADE_NOT_ALLOWED", infraerrors.Reason(err), "a suspended plan still counts")
}

// A redeem code or an admin assignment that restarts an ended plan subscription is not a plan purchase: the new
// term follows the group's limits. Extending a running plan keeps its allowance.
func TestRestartWithoutAPlanDropsTheOldPlansAllowance(t *testing.T) {
	f := newPlanChangeFixture(t)
	f.seed(f.pro, 40*24*time.Hour, 50) // ended 10 days ago
	_, _, err := f.svc.subscriptionSvc.AssignOrExtendSubscription(f.ctx, &AssignSubscriptionInput{UserID: f.userID, GroupID: planChangeGroupID, ValidityDays: 7, Notes: "redeem"})
	require.NoError(t, err)
	sub := f.subscription()
	require.Nil(t, sub.PlanID)
	require.Nil(t, sub.MonthlyLimitUSD)
}

func TestExtendingARunningPlanWithoutAPurchaseKeepsItsAllowance(t *testing.T) {
	g := newPlanChangeFixture(t)
	running := g.seed(g.pro, 5*24*time.Hour, 50)
	_, _, err := g.svc.subscriptionSvc.AssignOrExtendSubscription(g.ctx, &AssignSubscriptionInput{UserID: g.userID, GroupID: planChangeGroupID, ValidityDays: 7, Notes: "redeem"})
	require.NoError(t, err)
	extended := g.subscription()
	require.True(t, extended.ExpiresAt.Equal(running.ExpiresAt.AddDate(0, 0, 7)))
	require.Equal(t, g.pro.ID, *extended.PlanID)
	require.InDelta(t, 120, *extended.MonthlyLimitUSD, 1e-9)
}

func TestPlanOrderForAnUpgradeOrWithoutAPlanIsAllowed(t *testing.T) {
	f := newPlanChangeFixture(t)
	_, err := f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: f.plus.ID})
	require.NoError(t, err, "no subscription yet")

	f.seed(f.plus, 5*24*time.Hour, 10)
	_, err = f.svc.validateSubOrder(f.ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeSubscription, PlanID: f.pro.ID})
	require.NoError(t, err, "an upgrade")
}

func (s *subscriptionUserSubRepoStub) ExtendExpiry(_ context.Context, id int64, expiresAt time.Time) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.ExpiresAt = expiresAt
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdateStatus(_ context.Context, id int64, status string) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.Status = status
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdateNotes(_ context.Context, id int64, notes string) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.Notes = notes
	return nil
}
