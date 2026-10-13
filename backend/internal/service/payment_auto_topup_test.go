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

// autoTopUpPaymentRig is a payment service with a pending Stripe balance order of $20 that an automatic charge
// is waiting for, and automatic top-up attached.
type autoTopUpPaymentRig struct {
	svc      *PaymentService
	client   *dbent.Client
	order    *dbent.PaymentOrder
	auto     *AutoTopUpOrder
	repo     *fakeAutoTopUpRepo
	hook     *OtohaAutoTopUpService
	credited *float64
	codes    *paymentFulfillmentRedeemRepo
}

func newAutoTopUpPaymentRig(t *testing.T) *autoTopUpPaymentRig {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPending, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).SetPaymentType(payment.TypeStripe).SetProviderKey(payment.TypeStripe).
		SetAmount(20).SetPayAmount(20).
		ClearPlanID().ClearSubscriptionGroupID().ClearSubscriptionDays().ClearPaidAt().
		Save(ctx)
	require.NoError(t, err)

	rig := &autoTopUpPaymentRig{client: client, order: order, repo: newFakeAutoTopUpRepo(), codes: &paymentFulfillmentRedeemRepo{}}
	credited := 0.0
	rig.credited = &credited
	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID}}
	userRepo.updateBalanceFn = func(_ context.Context, _ int64, amount float64) error { credited += amount; return nil }
	redeemService := NewRedeemService(rig.codes, userRepo, nil, &paymentFulfillmentRedeemCacheStub{}, nil, client, nil, nil)
	rig.svc = &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}

	rig.repo.records[order.UserID] = &AutoTopUpRecord{UserID: order.UserID, Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100,
		ProviderInstanceID: "7", CustomerID: "cus_1", PaymentMethodID: "pm_1", CardBrand: "visa", CardLast4: "4242"}
	rig.repo.seedCharge(order.UserID, 20, AutoTopUpChargePending, time.Now())
	require.NoError(t, rig.repo.AttachOrder(ctx, 1, int64(order.ID)))

	rig.hook = NewOtohaAutoTopUpService(rig.repo, &fakeAutoTopUpPayments{available: true}, fakeAutoTopUpUsers{}, &fakeAutoTopUpMail{}, nil)
	rig.hook.spawn = func(f func()) { f() }
	rig.svc.SetAutoTopUp(rig.hook)
	rig.auto = &AutoTopUpOrder{ID: int64(order.ID), UserID: order.UserID, OutTradeNo: order.OutTradeNo, PayAmount: "20.00", PayAmountValue: 20, Currency: "USD", CreditedUSD: 20}
	return rig
}

func (r *autoTopUpPaymentRig) webhook(status string, meta map[string]string) *payment.PaymentNotification {
	return &payment.PaymentNotification{TradeNo: "pi_1", OrderID: r.order.OutTradeNo, Amount: 20, Status: status, Metadata: meta}
}

func (r *autoTopUpPaymentRig) chargeStatus(t *testing.T) string {
	t.Helper()
	charges := r.repo.chargesFor(r.order.UserID)
	require.Len(t, charges, 1)
	return charges[0].Status
}

func TestAutoTopUpChargeIsCreditedOnceWhetherTheAnswerOrTheWebhookComesFirst(t *testing.T) {
	ctx := context.Background()
	meta := map[string]string{"currency": "USD"}

	t.Run("the charge's own answer first", func(t *testing.T) {
		r := newAutoTopUpPaymentRig(t)
		require.NoError(t, r.svc.SettleAutoTopUp(ctx, r.auto, "pi_1"))
		require.NoError(t, r.svc.HandlePaymentNotification(ctx, r.webhook(payment.NotificationStatusSuccess, meta), payment.TypeStripe))
		require.NoError(t, r.svc.HandlePaymentNotification(ctx, r.webhook(payment.NotificationStatusSuccess, meta), payment.TypeStripe))

		require.InDelta(t, 20, *r.credited, 1e-9, "$20 is credited once")
		require.Equal(t, 1, r.codes.createCalls)
		order, err := r.client.PaymentOrder.Get(ctx, r.order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCompleted, order.Status)
		require.Equal(t, AutoTopUpChargeSucceeded, r.chargeStatus(t))
	})

	t.Run("the webhook first", func(t *testing.T) {
		r := newAutoTopUpPaymentRig(t)
		require.NoError(t, r.svc.HandlePaymentNotification(ctx, r.webhook(payment.NotificationStatusSuccess, meta), payment.TypeStripe))
		require.NoError(t, r.svc.SettleAutoTopUp(ctx, r.auto, "pi_1"))

		require.InDelta(t, 20, *r.credited, 1e-9)
		require.Equal(t, 1, r.codes.createCalls)
		require.Equal(t, AutoTopUpChargeSucceeded, r.chargeStatus(t))
	})
}

func TestAutoTopUpFailureWebhookTurnsItOff(t *testing.T) {
	ctx := context.Background()
	r := newAutoTopUpPaymentRig(t)

	require.NoError(t, r.svc.HandlePaymentNotification(ctx,
		r.webhook(payment.ProviderStatusFailed, map[string]string{payment.NotificationMetaFailureCode: "card_declined"}), payment.TypeStripe))

	require.Equal(t, AutoTopUpChargeFailed, r.chargeStatus(t))
	rec, _ := r.repo.Get(ctx, r.order.UserID)
	require.False(t, rec.Enabled)
	require.Equal(t, AutoTopUpReasonCardDeclined, rec.DisabledReason)
	require.Zero(t, *r.credited)
}

func TestAutoTopUpClosedOrderIsStillCreditedIfTheMoneyWasTaken(t *testing.T) {
	ctx := context.Background()
	r := newAutoTopUpPaymentRig(t)

	// Stripe could not be reached, so the order was closed; later its notification shows the charge went through.
	r.svc.CancelAutoTopUpOrder(ctx, r.auto, AutoTopUpReasonPaymentError)
	order, err := r.client.PaymentOrder.Get(ctx, r.order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, order.Status)
	require.NotNil(t, order.FailedReason)

	require.NoError(t, r.svc.HandlePaymentNotification(ctx, r.webhook(payment.NotificationStatusSuccess, map[string]string{"currency": "USD"}), payment.TypeStripe))
	require.InDelta(t, 20, *r.credited, 1e-9)
}

func TestAutoTopUpClosingAnOrderThatWasAlreadyPaidChangesNothing(t *testing.T) {
	ctx := context.Background()
	r := newAutoTopUpPaymentRig(t)
	require.NoError(t, r.svc.SettleAutoTopUp(ctx, r.auto, "pi_1"))

	r.svc.CancelAutoTopUpOrder(ctx, r.auto, AutoTopUpReasonPaymentError)
	order, err := r.client.PaymentOrder.Get(ctx, r.order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, order.Status)
}

func TestPaymentConfirmedByWebhookKeepsTheCardOfAPaymentThatAskedForIt(t *testing.T) {
	ctx := context.Background()
	r := newAutoTopUpPaymentRig(t)
	delete(r.repo.records, r.order.UserID)
	r.repo.records[r.order.UserID] = &AutoTopUpRecord{UserID: r.order.UserID, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100, ProviderInstanceID: "7", CustomerID: "cus_1"}
	gw := &fakeCardGateway{lookupCard: &payment.SavedCard{PaymentMethodID: "pm_paid", Brand: "visa", Last4: "4242", ExpMonth: 1, ExpYear: 2031}}
	r.hook.payments = &fakeAutoTopUpPayments{gateway: gw, available: true}

	require.NoError(t, r.svc.HandlePaymentNotification(ctx, r.webhook(payment.NotificationStatusSuccess, map[string]string{
		"currency": "USD", payment.NotificationMetaSaveCard: "1", payment.NotificationMetaCustomer: "cus_1", payment.NotificationMetaPaymentMethod: "pm_paid",
	}), payment.TypeStripe))

	rec, _ := r.repo.Get(ctx, r.order.UserID)
	require.Equal(t, "pm_paid", rec.PaymentMethodID)
	require.Equal(t, "4242", rec.CardLast4)
}
