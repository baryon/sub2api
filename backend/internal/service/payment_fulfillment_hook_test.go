//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type recordingFulfillmentHook struct {
	calls []PaymentFulfillment
}

func (h *recordingFulfillmentHook) OnPaymentFulfilled(_ context.Context, done PaymentFulfillment) {
	h.calls = append(h.calls, done)
}

func TestBalanceFulfillmentTellsTheHookOnceCompleted(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPaid, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		ClearSubscriptionGroupID().
		ClearSubscriptionDays().
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID}}
	userRepo.updateBalanceFn = func(context.Context, int64, float64) error { return nil }
	redeemService := NewRedeemService(&paymentFulfillmentRedeemRepo{}, userRepo, nil, &paymentFulfillmentRedeemCacheStub{}, nil, client, nil, nil)
	hook := &recordingFulfillmentHook{}
	svc := &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}
	svc.SetFulfillmentHook(hook)

	require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))
	require.Equal(t, []PaymentFulfillment{{OrderID: order.ID, UserID: order.UserID, OrderType: payment.OrderTypeBalance}}, hook.calls)

	// A repeated notification for the completed order changes nothing.
	require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))
	require.Len(t, hook.calls, 1)
}

func TestSubscriptionFulfillmentTellsTheHookTheGroup(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPaid, time.Now())

	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: *order.SubscriptionGroupID, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	hook := &recordingFulfillmentHook{}
	svc := &PaymentService{
		entClient:       client,
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, newSubscriptionUserSubRepoStub(), nil, nil, nil),
	}
	svc.SetFulfillmentHook(hook)

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	require.Len(t, hook.calls, 1)
	require.Equal(t, order.ID, hook.calls[0].OrderID)
	require.Equal(t, order.UserID, hook.calls[0].UserID)
	require.Equal(t, payment.OrderTypeSubscription, hook.calls[0].OrderType)
	require.NotNil(t, hook.calls[0].SubscriptionGroupID)
	require.Equal(t, *order.SubscriptionGroupID, *hook.calls[0].SubscriptionGroupID)
}

func TestFailedFulfillmentDoesNotTellTheHook(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPaid, time.Now())

	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: *order.SubscriptionGroupID, Status: "disabled", SubscriptionType: SubscriptionTypeSubscription},
	}
	hook := &recordingFulfillmentHook{}
	svc := &PaymentService{
		entClient:       client,
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, newSubscriptionUserSubRepoStub(), nil, nil, nil),
	}
	svc.SetFulfillmentHook(hook)

	require.Error(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	require.Empty(t, hook.calls)
}

type contextCapturingHook struct {
	ctxErr      error
	hasDeadline bool
}

func (h *contextCapturingHook) OnPaymentFulfilled(ctx context.Context, _ PaymentFulfillment) {
	h.ctxErr = ctx.Err()
	_, h.hasDeadline = ctx.Deadline()
}

func TestFulfillmentHookGetsItsOwnBoundedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the webhook client went away right after completion
	hook := &contextCapturingHook{}
	svc := &PaymentService{}
	svc.SetFulfillmentHook(hook)

	svc.runFulfillmentHook(ctx, &dbent.PaymentOrder{ID: 1, UserID: 2, OrderType: payment.OrderTypeBalance})
	require.NoError(t, hook.ctxErr, "a cancelled request does not cancel the hook")
	require.True(t, hook.hasDeadline, "the hook cannot hold the payment up indefinitely")
}
