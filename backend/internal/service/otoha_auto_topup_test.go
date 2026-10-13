//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

const autoTopUpTestUser int64 = 7

type fakeAutoTopUpUsers struct{}

func (fakeAutoTopUpUsers) GetByID(_ context.Context, id int64) (*User, error) {
	return &User{ID: id, Email: "buyer@example.com", Username: "Buyer"}, nil
}

type autoTopUpHarness struct {
	repo *fakeAutoTopUpRepo
	pay  *fakeAutoTopUpPayments
	gw   *fakeCardGateway
	mail *fakeAutoTopUpMail
	svc  *OtohaAutoTopUpService
	now  time.Time
}

// newAutoTopUpHarness has a user with a saved card, auto top-up on (below $5, add $20, at most $100 a month) and
// a balance of $3, so one deduction away from needing money.
func newAutoTopUpHarness(t *testing.T) *autoTopUpHarness {
	t.Helper()
	h := &autoTopUpHarness{
		repo: newFakeAutoTopUpRepo(),
		gw: &fakeCardGateway{chargeResult: &payment.SavedCardChargeResult{
			Status: payment.SavedCardChargeSucceeded, TradeNo: "pi_auto_1"}},
		mail: &fakeAutoTopUpMail{},
		now:  time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
	}
	h.pay = &fakeAutoTopUpPayments{gateway: h.gw, available: true, min: 1, max: 1000}
	h.pay.onSettle = func(order *AutoTopUpOrder) { h.repo.setBalance(autoTopUpTestUser, 3+order.CreditedUSD) }
	h.repo.records[autoTopUpTestUser] = &AutoTopUpRecord{
		UserID: autoTopUpTestUser, Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100,
		ProviderInstanceID: "7", CustomerID: "cus_1", PaymentMethodID: "pm_1", CardBrand: "visa", CardLast4: "4242",
		CardExpMonth: 12, CardExpYear: 2030,
	}
	h.repo.balances[autoTopUpTestUser] = 3
	h.svc = h.newService()
	return h
}

// newService makes another server process on the same database: its own memory, the same rows.
func (h *autoTopUpHarness) newService() *OtohaAutoTopUpService {
	svc := NewOtohaAutoTopUpService(h.repo, h.pay, fakeAutoTopUpUsers{}, h.mail,
		fakeAutoTopUpSettings{values: map[string]string{SettingKeyFrontendURL: "https://account.otohaai.com/"}})
	svc.now = func() time.Time { return h.now }
	svc.spawn = func(f func()) { f() }
	return svc
}

func (h *autoTopUpHarness) onlyCharge(t *testing.T) AutoTopUpCharge {
	t.Helper()
	charges := h.repo.chargesFor(autoTopUpTestUser)
	require.Len(t, charges, 1)
	return charges[0]
}

func TestAutoTopUpChargesTheSavedCardWhenTheBalanceFallsBelowTheThreshold(t *testing.T) {
	h := newAutoTopUpHarness(t)

	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4.2)

	require.Equal(t, 1, h.gw.chargeCount())
	charge := h.gw.charges[0]
	require.Equal(t, "20.00", charge.Amount)
	require.Equal(t, "cus_1", charge.CustomerID)
	require.Equal(t, "pm_1", charge.PaymentMethodID)
	require.Equal(t, h.pay.orders[0].OutTradeNo, charge.OrderID, "the payment notification finds the order by this")
	require.Equal(t, []string{h.pay.orders[0].OutTradeNo + "/pi_auto_1"}, h.pay.settled, "the money is credited through the normal fulfillment")
	got := h.onlyCharge(t)
	require.Equal(t, AutoTopUpChargeSucceeded, got.Status)
	require.Equal(t, h.pay.orders[0].ID, *got.OrderID)
	require.Empty(t, h.mail.events(), "a good charge sends no warning; its receipt is the normal recharge email")
}

func TestAutoTopUpDoesNothingWhenItShouldNot(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(h *autoTopUpHarness)
		newBalance float64
	}{
		{name: "balance equal to the threshold", newBalance: 5},
		{name: "balance above the threshold", newBalance: 12},
		{name: "turned off", newBalance: 1, setup: func(h *autoTopUpHarness) { h.repo.records[autoTopUpTestUser].Enabled = false }},
		{name: "no saved card", newBalance: 1, setup: func(h *autoTopUpHarness) { h.repo.records[autoTopUpTestUser].PaymentMethodID = "" }},
		{name: "never set up", newBalance: 1, setup: func(h *autoTopUpHarness) { delete(h.repo.records, autoTopUpTestUser) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAutoTopUpHarness(t)
			if tt.setup != nil {
				tt.setup(h)
			}
			svc := h.newService()
			svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, tt.newBalance)
			require.Zero(t, h.gw.chargeCount())
			require.Empty(t, h.repo.chargesFor(autoTopUpTestUser))
		})
	}
}

func TestAutoTopUpTwoDeductionsAtOnceMakeOneCharge(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.started = make(chan struct{})
	h.gw.release = make(chan struct{})
	// A second server process handling the other deduction: it has its own memory, so only the database stops it.
	other := h.newService()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	}()
	<-h.gw.started // the first charge is out with Stripe

	other.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 3.5) // returns at once: a charge is in flight
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 3.4) // same process: also nothing to do

	close(h.gw.release)
	wg.Wait()
	require.Equal(t, 1, h.gw.chargeCount(), "one low balance, one charge")
	require.Len(t, h.repo.chargesFor(autoTopUpTestUser), 1)
}

func TestAutoTopUpDoesNotChargeAgainOnceTheBalanceIsBackAboveTheThreshold(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, 1, h.gw.chargeCount())

	// A deduction that was already on its way when the first top-up landed still reports a low balance.
	h.newService().OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, 1, h.gw.chargeCount(), "the real balance is $23 now")
}

func TestAutoTopUpStopsAtTheMonthlyCap(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.seedCharge(autoTopUpTestUser, 80, AutoTopUpChargeSucceeded, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	// Last month's charges and failed ones do not count against this month.
	h.repo.seedCharge(autoTopUpTestUser, 100, AutoTopUpChargeSucceeded, time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC))
	h.repo.seedCharge(autoTopUpTestUser, 50, AutoTopUpChargeFailed, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))

	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, 1, h.gw.chargeCount(), "$80 used plus $20 reaches the $100 cap exactly, which is allowed")

	h.repo.setBalance(autoTopUpTestUser, 3)
	h.svc = h.newService()
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 2)
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 1)
	require.Equal(t, 1, h.gw.chargeCount(), "the cap is reached: no more charges this month")
	require.Equal(t, []string{NotificationEmailEventAutoTopUpCapReached}, h.mail.events(), "the user hears about it once")
	sent := h.mail.sent[0]
	require.Equal(t, "otoha_auto_topup_cap", sent.SourceType)
	require.Equal(t, "7:2026-10", sent.SourceID, "the email is sent once per user and month")
	require.Equal(t, "100.00", sent.Variables["monthly_cap"])
	require.Equal(t, "https://account.otohaai.com/otoha/account#auto-topup", sent.Variables["manage_url"])
}

func TestAutoTopUpChargesAgainInTheNextMonth(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.seedCharge(autoTopUpTestUser, 100, AutoTopUpChargeSucceeded, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Zero(t, h.gw.chargeCount())

	h.now = time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC)
	h.svc = h.newService()
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, 1, h.gw.chargeCount())
}

func TestAutoTopUpTurnsItselfOffWhenTheChargeFails(t *testing.T) {
	tests := []struct {
		name         string
		result       *payment.SavedCardChargeResult
		err          error
		wantStatus   string
		wantCategory string
		wantDetail   string
	}{
		{
			name:         "card declined",
			result:       &payment.SavedCardChargeResult{Status: payment.SavedCardChargeDeclined, Code: "insufficient_funds", Message: "no money"},
			wantStatus:   AutoTopUpChargeFailed,
			wantCategory: AutoTopUpReasonCardDeclined,
			wantDetail:   "insufficient_funds",
		},
		{
			name:         "bank asks the customer to confirm",
			result:       &payment.SavedCardChargeResult{Status: payment.SavedCardChargeActionRequired, Code: "authentication_required", TradeNo: "pi_sca"},
			wantStatus:   AutoTopUpChargeRequiresAction,
			wantCategory: AutoTopUpReasonAuthenticationRequired,
			wantDetail:   "authentication_required",
		},
		{
			name:         "Stripe cannot be reached",
			err:          errors.New("dial tcp: i/o timeout"),
			wantStatus:   AutoTopUpChargeFailed,
			wantCategory: AutoTopUpReasonPaymentError,
			wantDetail:   AutoTopUpReasonPaymentError,
		},
		{
			name:         "any other failure",
			result:       &payment.SavedCardChargeResult{Status: payment.SavedCardChargeFailed},
			wantStatus:   AutoTopUpChargeFailed,
			wantCategory: AutoTopUpReasonPaymentError,
			wantDetail:   AutoTopUpReasonPaymentError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAutoTopUpHarness(t)
			h.gw.chargeResult, h.gw.chargeErr = tt.result, tt.err

			h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)

			charge := h.onlyCharge(t)
			require.Equal(t, tt.wantStatus, charge.Status)
			require.Equal(t, tt.wantDetail, charge.Reason)
			rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
			require.False(t, rec.Enabled, "a failed charge turns automatic top-up off")
			require.Equal(t, tt.wantCategory, rec.DisabledReason)
			require.NotNil(t, rec.DisabledAt)
			require.Empty(t, h.pay.settled, "nothing is credited")
			require.Equal(t, []string{h.pay.orders[0].OutTradeNo + "/" + tt.wantCategory}, h.pay.cancelled, "the unpaid order is closed")

			require.Equal(t, []string{NotificationEmailEventAutoTopUpStopped}, h.mail.events())
			mail := h.mail.sent[0]
			require.Equal(t, "buyer@example.com", mail.RecipientEmail)
			require.Equal(t, "otoha_auto_topup", mail.SourceType)
			require.NotEmpty(t, mail.Variables["reason_text"])
			require.Equal(t, "Visa ending 4242", mail.Variables["card_summary"], "the card is named by brand and last four digits only")
			require.Equal(t, "https://account.otohaai.com/otoha/account#auto-topup", mail.Variables["manage_url"])

			// Off means off: the next low balance does not try again.
			h.repo.setBalance(autoTopUpTestUser, 2)
			h.newService().OnBalanceDeducted(context.Background(), autoTopUpTestUser, 2)
			require.Equal(t, 1, h.gw.chargeCount())
		})
	}
}

func TestAutoTopUpEmailTextIsPlainAndPerLanguage(t *testing.T) {
	for _, reason := range []string{AutoTopUpReasonAuthenticationRequired, AutoTopUpReasonCardDeclined, AutoTopUpReasonPaymentError} {
		en, zh := autoTopUpReasonText("en", reason), autoTopUpReasonText("zh", reason)
		require.NotEmpty(t, en)
		require.NotEmpty(t, zh)
		require.NotEqual(t, en, zh)
		require.NotContains(t, en, reason, "no internal codes in what a user reads")
	}
}

func TestAutoTopUpKeepsAProcessingChargePendingUntilStripeSettles(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.chargeResult = &payment.SavedCardChargeResult{Status: payment.SavedCardChargeProcessing, TradeNo: "pi_slow"}

	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)

	require.Equal(t, AutoTopUpChargePending, h.onlyCharge(t).Status)
	require.Empty(t, h.pay.settled)
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.True(t, rec.Enabled)
	h.newService().OnBalanceDeducted(context.Background(), autoTopUpTestUser, 3)
	require.Equal(t, 1, h.gw.chargeCount(), "a pending charge is not repeated")

	// Stripe's notification arrives and the order is fulfilled.
	order := &dbent.PaymentOrder{ID: h.pay.orders[0].ID, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance}
	h.svc.OnPaymentConfirmed(context.Background(), order, map[string]string{"currency": "USD"})
	require.Equal(t, AutoTopUpChargeSucceeded, h.onlyCharge(t).Status)
}

func TestAutoTopUpProcessingChargeThatLaterFailsTurnsItOff(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.chargeResult = &payment.SavedCardChargeResult{Status: payment.SavedCardChargeProcessing, TradeNo: "pi_slow"}
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)

	order := &dbent.PaymentOrder{ID: h.pay.orders[0].ID, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance}
	h.svc.OnPaymentFailed(context.Background(), order, "card_declined")

	require.Equal(t, AutoTopUpChargeFailed, h.onlyCharge(t).Status)
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.False(t, rec.Enabled)
	require.Equal(t, AutoTopUpReasonCardDeclined, rec.DisabledReason)
	require.Equal(t, []string{NotificationEmailEventAutoTopUpStopped}, h.mail.events())

	// A repeated failure notification changes nothing and sends nothing more.
	h.svc.OnPaymentFailed(context.Background(), order, "card_declined")
	require.Equal(t, []string{NotificationEmailEventAutoTopUpStopped}, h.mail.events())
}

func TestAutoTopUpFailureNoticeForAnOrderThatIsNotAnAutoTopUpIsIgnored(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.svc.OnPaymentFailed(context.Background(), &dbent.PaymentOrder{ID: 999, UserID: autoTopUpTestUser}, "card_declined")
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.True(t, rec.Enabled, "a failed payment the user started themselves does not touch automatic top-up")
	require.Empty(t, h.mail.events())
}

func TestAutoTopUpDoesNotChargeTwiceWhileFulfillmentIsStillOpen(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.pay.settleErr = errors.New("database busy")

	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, AutoTopUpChargePending, h.onlyCharge(t).Status, "the money was taken but not credited yet")
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.True(t, rec.Enabled, "the card worked; nothing to switch off")
	require.Empty(t, h.mail.events())

	h.newService().OnBalanceDeducted(context.Background(), autoTopUpTestUser, 3)
	require.Equal(t, 1, h.gw.chargeCount())

	// The webhook retries the fulfillment and reports the order as paid.
	order := &dbent.PaymentOrder{ID: h.pay.orders[0].ID, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance}
	h.svc.OnPaymentConfirmed(context.Background(), order, nil)
	require.Equal(t, AutoTopUpChargeSucceeded, h.onlyCharge(t).Status)
}

func TestAutoTopUpWebhookAfterTheSynchronousResultChangesNothing(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	before := h.onlyCharge(t)

	order := &dbent.PaymentOrder{ID: h.pay.orders[0].ID, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance}
	h.svc.OnPaymentConfirmed(context.Background(), order, map[string]string{"currency": "USD"})
	h.svc.OnPaymentConfirmed(context.Background(), order, map[string]string{"currency": "USD"})

	require.Equal(t, before, h.onlyCharge(t))
	require.Equal(t, 1, h.gw.chargeCount())
	require.Len(t, h.pay.settled, 1)
}

func TestAutoTopUpUnreachableStripeThatLaterTurnsOutPaidIsCountedAsPaid(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.chargeResult, h.gw.chargeErr = nil, errors.New("timeout")
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, AutoTopUpChargeFailed, h.onlyCharge(t).Status)

	// Stripe did take the money; its notification credits the order through the normal path.
	order := &dbent.PaymentOrder{ID: h.pay.orders[0].ID, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance}
	h.svc.OnPaymentConfirmed(context.Background(), order, nil)
	require.Equal(t, AutoTopUpChargeSucceeded, h.onlyCharge(t).Status, "counts against the monthly cap")
}

func TestAutoTopUpAStuckPendingChargeIsClearedAfterAWhile(t *testing.T) {
	h := newAutoTopUpHarness(t)
	stuck := h.now.Add(-autoTopUpPendingStale - time.Minute)
	h.repo.seedCharge(autoTopUpTestUser, 20, AutoTopUpChargePending, stuck)

	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, 1, h.gw.chargeCount())
}

func TestAutoTopUpWithoutAUsableCardGatewayTurnsItOff(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.pay.gatewayErr = errors.New("payment provider instance is disabled")

	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)

	require.Zero(t, h.gw.chargeCount())
	require.Equal(t, AutoTopUpChargeFailed, h.onlyCharge(t).Status)
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.False(t, rec.Enabled)
	require.Equal(t, AutoTopUpReasonPaymentError, rec.DisabledReason)
	require.Equal(t, []string{NotificationEmailEventAutoTopUpStopped}, h.mail.events())
}

func TestAutoTopUpSettingsAreValidated(t *testing.T) {
	valid := AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100}
	tests := []struct {
		name   string
		change func(s *AutoTopUpSettings)
		ok     bool
	}{
		{name: "defaults", change: func(*AutoTopUpSettings) {}, ok: true},
		{name: "cap equal to one top-up", change: func(s *AutoTopUpSettings) { s.MonthlyCapUSD = 20 }, ok: true},
		{name: "threshold zero", change: func(s *AutoTopUpSettings) { s.ThresholdUSD = 0 }},
		{name: "threshold negative", change: func(s *AutoTopUpSettings) { s.ThresholdUSD = -1 }},
		{name: "threshold too high", change: func(s *AutoTopUpSettings) { s.ThresholdUSD = 1001 }},
		{name: "amount below the site minimum", change: func(s *AutoTopUpSettings) { s.AmountUSD = 0.5 }},
		{name: "amount above the site maximum", change: func(s *AutoTopUpSettings) { s.AmountUSD = 1001; s.MonthlyCapUSD = 5000 }},
		{name: "cap below one top-up", change: func(s *AutoTopUpSettings) { s.MonthlyCapUSD = 19.99 }},
		{name: "cap too high", change: func(s *AutoTopUpSettings) { s.MonthlyCapUSD = 10001 }},
		{name: "fractions of a cent", change: func(s *AutoTopUpSettings) { s.AmountUSD = 20.001 }},
		{name: "not a number", change: func(s *AutoTopUpSettings) { s.ThresholdUSD = math.NaN() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAutoTopUpHarness(t)
			in := valid
			tt.change(&in)
			_, err := h.svc.UpdateSettings(context.Background(), autoTopUpTestUser, in)
			if tt.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrAutoTopUpInvalidSettings)
			rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
			require.Equal(t, 20.0, rec.AmountUSD, "nothing is saved when the settings are refused")
		})
	}
}

func TestAutoTopUpCannotBeTurnedOnWithoutACard(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.records[autoTopUpTestUser].PaymentMethodID = ""
	h.repo.records[autoTopUpTestUser].Enabled = false

	_, err := h.svc.UpdateSettings(context.Background(), autoTopUpTestUser, AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100})
	require.ErrorIs(t, err, ErrAutoTopUpNeedsCard)

	// Changing the amounts while it stays off needs no card.
	_, err = h.svc.UpdateSettings(context.Background(), autoTopUpTestUser, AutoTopUpSettings{Enabled: false, ThresholdUSD: 10, AmountUSD: 30, MonthlyCapUSD: 60})
	require.NoError(t, err)
}

func TestAutoTopUpCannotBeTurnedOnWhenCardPaymentsAreUnavailable(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.records[autoTopUpTestUser].Enabled = false
	h.pay.available = false
	_, err := h.svc.UpdateSettings(context.Background(), autoTopUpTestUser, AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100})
	require.ErrorIs(t, err, ErrAutoTopUpUnavailable)
}

func TestAutoTopUpTurningItOnAgainClearsTheReasonItWasStopped(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.chargeResult = &payment.SavedCardChargeResult{Status: payment.SavedCardChargeActionRequired, Code: "authentication_required"}
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	state, err := h.svc.State(context.Background(), autoTopUpTestUser)
	require.NoError(t, err)
	require.False(t, state.Enabled)
	require.Equal(t, AutoTopUpReasonAuthenticationRequired, state.DisabledReason)

	state, err = h.svc.UpdateSettings(context.Background(), autoTopUpTestUser, AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100})
	require.NoError(t, err)
	require.True(t, state.Enabled)
	require.Empty(t, state.DisabledReason)
	require.Nil(t, state.DisabledAt)
}

func TestAutoTopUpRaisingTheCapTakesEffectAtOnce(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.seedCharge(autoTopUpTestUser, 90, AutoTopUpChargeSucceeded, h.now.Add(-time.Hour))
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Zero(t, h.gw.chargeCount())

	_, err := h.svc.UpdateSettings(context.Background(), autoTopUpTestUser, AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 200})
	require.NoError(t, err)
	h.svc.OnBalanceDeducted(context.Background(), autoTopUpTestUser, 4)
	require.Equal(t, 1, h.gw.chargeCount())
}

func TestAutoTopUpStateHasDefaultsBeforeAnythingIsSet(t *testing.T) {
	h := newAutoTopUpHarness(t)
	delete(h.repo.records, autoTopUpTestUser)

	state, err := h.svc.State(context.Background(), autoTopUpTestUser)
	require.NoError(t, err)
	require.False(t, state.Enabled, "off by default")
	require.Equal(t, 5.0, state.ThresholdUSD)
	require.Equal(t, 20.0, state.AmountUSD)
	require.Equal(t, 100.0, state.MonthlyCapUSD)
	require.Nil(t, state.Card)
	require.Nil(t, state.LastTopUp)
	require.Equal(t, "https://account.otohaai.com/otoha/account#auto-topup", state.ManageURL)
}

func TestAutoTopUpStateShape(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.seedCharge(autoTopUpTestUser, 20, AutoTopUpChargeSucceeded, time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC))
	h.repo.seedCharge(autoTopUpTestUser, 20, AutoTopUpChargeFailed, time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC))
	h.repo.seedCharge(autoTopUpTestUser, 20, AutoTopUpChargeSucceeded, time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC))

	state, err := h.svc.State(context.Background(), autoTopUpTestUser)
	require.NoError(t, err)
	raw, err := json.Marshal(state)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, true, got["enabled"])
	require.Equal(t, 5.0, got["threshold_usd"])
	require.Equal(t, 20.0, got["amount_usd"])
	require.Equal(t, 100.0, got["monthly_cap_usd"])
	require.Equal(t, 20.0, got["used_this_month_usd"], "only this month's successful charges count")
	require.Equal(t, "", got["disabled_reason"])
	require.Equal(t, "https://account.otohaai.com/otoha/account#auto-topup", got["manage_url"])
	require.Equal(t, map[string]any{"brand": "visa", "last4": "4242", "exp_month": 12.0, "exp_year": 2030.0}, got["card"])
	last := got["last_top_up"].(map[string]any)
	require.Equal(t, "failed", last["result"])
	require.Equal(t, 20.0, last["amount_usd"])
	require.Equal(t, "2026-10-09T08:00:00Z", last["at"])
	require.NotContains(t, string(raw), "pm_1", "Stripe's ids never leave the server")
	require.NotContains(t, string(raw), "cus_1")
}

func TestAutoTopUpCardSetupStartsAndFinishes(t *testing.T) {
	h := newAutoTopUpHarness(t)
	delete(h.repo.records, autoTopUpTestUser)

	start, err := h.svc.StartCardSetup(context.Background(), autoTopUpTestUser)
	require.NoError(t, err)
	require.Equal(t, "seti_1_secret", start.ClientSecret)
	require.Equal(t, "pk_test_fake", start.PublishableKey)
	require.Len(t, h.gw.customers, 1)
	require.Equal(t, "buyer@example.com", h.gw.customers[0].Email)
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.Equal(t, "7", rec.ProviderInstanceID)
	require.Equal(t, "cus_"+h.gw.customers[0].Key, rec.CustomerID)

	// A second start reuses the customer.
	_, err = h.svc.StartCardSetup(context.Background(), autoTopUpTestUser)
	require.NoError(t, err)
	require.Len(t, h.gw.customers, 1)

	h.gw.finishCard = &payment.SavedCard{PaymentMethodID: "pm_new", Brand: "mastercard", Last4: "4444", ExpMonth: 1, ExpYear: 2031}
	state, err := h.svc.FinishCardSetup(context.Background(), autoTopUpTestUser, "seti_1")
	require.NoError(t, err)
	require.Equal(t, &OtohaAutoTopUpCard{Brand: "mastercard", Last4: "4444", ExpMonth: 1, ExpYear: 2031}, state.Card)
	require.False(t, state.Enabled, "saving a card does not turn automatic top-up on")
}

func TestAutoTopUpNewCardReplacesAndRemovesTheOldOne(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.finishCard = &payment.SavedCard{PaymentMethodID: "pm_new", Brand: "visa", Last4: "1111", ExpMonth: 2, ExpYear: 2032}

	_, err := h.svc.FinishCardSetup(context.Background(), autoTopUpTestUser, "seti_1")
	require.NoError(t, err)
	require.Equal(t, []string{"pm_1"}, h.gw.removed, "Stripe stops keeping the old card")
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.Equal(t, "pm_new", rec.PaymentMethodID)
	require.True(t, rec.Enabled, "an enabled setup stays enabled with the new card")
}

func TestAutoTopUpCardSetupThatDidNotFinishSavesNothing(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.gw.finishErr = errors.New("setup intent is requires_action, not succeeded")

	_, err := h.svc.FinishCardSetup(context.Background(), autoTopUpTestUser, "seti_1")
	require.ErrorIs(t, err, ErrAutoTopUpCardNotSaved)
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.Equal(t, "pm_1", rec.PaymentMethodID)

	// Nobody started a setup for this user.
	delete(h.repo.records, autoTopUpTestUser)
	_, err = h.svc.FinishCardSetup(context.Background(), autoTopUpTestUser, "seti_1")
	require.ErrorIs(t, err, ErrAutoTopUpCardNotSaved)
}

func TestAutoTopUpRemovingTheCardTurnsItOff(t *testing.T) {
	h := newAutoTopUpHarness(t)

	state, err := h.svc.RemoveCard(context.Background(), autoTopUpTestUser)
	require.NoError(t, err)
	require.Nil(t, state.Card)
	require.False(t, state.Enabled)
	require.Equal(t, []string{"pm_1"}, h.gw.removed)

	h.newService().OnBalanceDeducted(context.Background(), autoTopUpTestUser, 1)
	require.Zero(t, h.gw.chargeCount())
}

func TestAutoTopUpKeepsTheCardOfAPaymentThatAskedForIt(t *testing.T) {
	h := newAutoTopUpHarness(t)
	delete(h.repo.records, autoTopUpTestUser)
	h.repo.records[autoTopUpTestUser] = &AutoTopUpRecord{UserID: autoTopUpTestUser, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100, ProviderInstanceID: "7", CustomerID: "cus_1"}
	h.gw.lookupCard = &payment.SavedCard{PaymentMethodID: "pm_paid", Brand: "amex", Last4: "0005", ExpMonth: 5, ExpYear: 2029}
	instance := "7"
	order := &dbent.PaymentOrder{ID: 50, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance, ProviderInstanceID: &instance}

	h.svc.OnPaymentConfirmed(context.Background(), order, map[string]string{
		payment.NotificationMetaSaveCard: "1", payment.NotificationMetaCustomer: "cus_1", payment.NotificationMetaPaymentMethod: "pm_paid",
	})

	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.Equal(t, "pm_paid", rec.PaymentMethodID)
	require.Equal(t, "amex", rec.CardBrand)
	require.False(t, rec.Enabled, "saving the card does not turn automatic top-up on")

	// A payment that did not ask to keep its card keeps nothing.
	h.repo.records[autoTopUpTestUser].PaymentMethodID = ""
	h.svc.OnPaymentConfirmed(context.Background(), order, map[string]string{payment.NotificationMetaPaymentMethod: "pm_paid", payment.NotificationMetaCustomer: "cus_1"})
	rec, _ = h.repo.Get(context.Background(), autoTopUpTestUser)
	require.Empty(t, rec.PaymentMethodID)
}

func TestAutoTopUpWillNotKeepACardOfAnotherCustomer(t *testing.T) {
	h := newAutoTopUpHarness(t)
	h.repo.records[autoTopUpTestUser].PaymentMethodID = ""
	h.gw.lookupErr = errors.New("stripe payment method belongs to another customer")
	instance := "7"
	order := &dbent.PaymentOrder{ID: 50, UserID: autoTopUpTestUser, OrderType: payment.OrderTypeBalance, ProviderInstanceID: &instance}

	h.svc.OnPaymentConfirmed(context.Background(), order, map[string]string{
		payment.NotificationMetaSaveCard: "1", payment.NotificationMetaCustomer: "cus_1", payment.NotificationMetaPaymentMethod: "pm_other",
	})
	rec, _ := h.repo.Get(context.Background(), autoTopUpTestUser)
	require.Empty(t, rec.PaymentMethodID)
}

func TestAutoTopUpCustomerForAPaymentThatKeepsTheCard(t *testing.T) {
	h := newAutoTopUpHarness(t)
	delete(h.repo.records, autoTopUpTestUser)

	id, err := h.svc.CustomerForCardSave(context.Background(), autoTopUpTestUser, "buyer@example.com", "7", h.gw)
	require.NoError(t, err)
	require.Equal(t, "cus_"+h.gw.customers[0].Key, id)
	id2, err := h.svc.CustomerForCardSave(context.Background(), autoTopUpTestUser, "buyer@example.com", "7", h.gw)
	require.NoError(t, err)
	require.Equal(t, id, id2)
	require.Len(t, h.gw.customers, 1, "the customer is created once")
}
