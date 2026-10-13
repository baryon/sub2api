//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type autoTopUpRepoFixture struct {
	repo   service.AutoTopUpRepository
	userID int64
	now    time.Time
}

func newAutoTopUpRepoFixture(t *testing.T, balance float64) *autoTopUpRepoFixture {
	t.Helper()
	ctx := context.Background()
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("auto-topup-%d@example.com", time.Now().UnixNano()), Balance: balance,
	})
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM otoha_auto_topup_charges WHERE user_id = $1",
			"DELETE FROM otoha_auto_topups WHERE user_id = $1",
			"DELETE FROM payment_orders WHERE user_id = $1",
			"DELETE FROM users WHERE id = $1",
		} {
			_, err := integrationDB.ExecContext(ctx, q, user.ID)
			require.NoError(t, err, q)
		}
	})
	f := &autoTopUpRepoFixture{repo: NewOtohaAutoTopUpRepository(integrationDB), userID: user.ID, now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	_, err := f.repo.SaveSettings(ctx, f.userID, service.AutoTopUpSettings{Enabled: false, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100})
	require.NoError(t, err)
	require.NoError(t, f.repo.SaveCard(ctx, f.userID, service.AutoTopUpCard{
		InstanceID: "7", CustomerID: "cus_1", PaymentMethodID: "pm_1", Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030,
	}))
	_, err = f.repo.SaveSettings(ctx, f.userID, service.AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100})
	require.NoError(t, err)
	return f
}

func (f *autoTopUpRepoFixture) claim(t *testing.T) (*service.AutoTopUpClaim, service.AutoTopUpSkip) {
	t.Helper()
	monthStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	claim, skip, err := f.repo.BeginCharge(context.Background(), service.AutoTopUpClaimRequest{
		UserID: f.userID, Now: f.now, StaleBefore: f.now.Add(-15 * time.Minute), MonthStart: monthStart, MonthEnd: monthStart.AddDate(0, 1, 0),
	})
	require.NoError(t, err)
	return claim, skip
}

func (f *autoTopUpRepoFixture) setBalance(t *testing.T, balance float64) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), "UPDATE users SET balance = $2 WHERE id = $1", f.userID, balance)
	require.NoError(t, err)
}

func (f *autoTopUpRepoFixture) order(t *testing.T) int64 {
	t.Helper()
	o, err := integrationEntClient.PaymentOrder.Create().
		SetUserID(f.userID).SetUserEmail("auto@example.com").SetUserName("auto").SetAmount(20).SetPayAmount(20).
		SetRechargeCode(fmt.Sprintf("PAY-AUTO-%d", time.Now().UnixNano())).SetOutTradeNo(fmt.Sprintf("sub2_auto%d", time.Now().UnixNano())).
		SetPaymentType(payment.TypeStripe).SetPaymentTradeNo("").SetOrderType(payment.OrderTypeBalance).SetStatus("PENDING").
		SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("auto-topup").SetSrcHost("auto-topup").Save(context.Background())
	require.NoError(t, err)
	return int64(o.ID)
}

func TestAutoTopUpRepoTwoRequestsAtOnceStartOneCharge(t *testing.T) {
	f := newAutoTopUpRepoFixture(t, 3)

	const workers = 8
	skips := make([]service.AutoTopUpSkip, workers)
	claims := make([]*service.AutoTopUpClaim, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			monthStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			claim, skip, err := f.repo.BeginCharge(context.Background(), service.AutoTopUpClaimRequest{
				UserID: f.userID, Now: f.now, StaleBefore: f.now.Add(-15 * time.Minute), MonthStart: monthStart, MonthEnd: monthStart.AddDate(0, 1, 0),
			})
			require.NoError(t, err)
			claims[i], skips[i] = claim, skip
		}(i)
	}
	close(start)
	wg.Wait()

	started := 0
	for i := range workers {
		if skips[i] == service.AutoTopUpSkipNone {
			started++
			require.NotNil(t, claims[i].Charge)
		} else {
			require.Equal(t, service.AutoTopUpSkipInFlight, skips[i])
		}
	}
	require.Equal(t, 1, started, "the database lets one charge start")
}

func TestAutoTopUpRepoNoChargeWhenOffWithoutCardOrBalanceIsEnough(t *testing.T) {
	ctx := context.Background()
	f := newAutoTopUpRepoFixture(t, 5)
	_, skip := f.claim(t)
	require.Equal(t, service.AutoTopUpSkipBalanceOK, skip, "the real balance is at the threshold")

	f.setBalance(t, 1)
	require.NoError(t, f.repo.Disable(ctx, f.userID, service.AutoTopUpReasonCardDeclined, f.now))
	_, skip = f.claim(t)
	require.Equal(t, service.AutoTopUpSkipNotEnabled, skip)

	_, err := f.repo.SaveSettings(ctx, f.userID, service.AutoTopUpSettings{Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100})
	require.NoError(t, err)
	require.NoError(t, f.repo.ClearCard(ctx, f.userID))
	_, skip = f.claim(t)
	require.Equal(t, service.AutoTopUpSkipNotEnabled, skip, "removing the card turns it off")
}

func TestAutoTopUpRepoMonthlyCap(t *testing.T) {
	ctx := context.Background()
	f := newAutoTopUpRepoFixture(t, 1)
	for i := 0; i < 5; i++ {
		claim, skip := f.claim(t)
		require.Equal(t, service.AutoTopUpSkipNone, skip, "charge %d is within the $100 cap", i+1)
		require.NoError(t, f.repo.FinishCharge(ctx, claim.Charge.ID, service.AutoTopUpChargeSucceeded, "", f.now))
	}
	claim, skip := f.claim(t)
	require.Equal(t, service.AutoTopUpSkipCapReached, skip)
	require.Equal(t, 100.0, claim.UsedUSD)

	// Failed charges do not count, and the next month starts again.
	_, err := integrationDB.ExecContext(ctx, "UPDATE otoha_auto_topup_charges SET created_at = $2 WHERE user_id = $1", f.userID, f.now.AddDate(0, -1, 0))
	require.NoError(t, err)
	_, skip = f.claim(t)
	require.Equal(t, service.AutoTopUpSkipNone, skip)
}

func TestAutoTopUpRepoStalePendingChargeIsClearedAndSettleByOrder(t *testing.T) {
	ctx := context.Background()
	f := newAutoTopUpRepoFixture(t, 1)
	claim, skip := f.claim(t)
	require.Equal(t, service.AutoTopUpSkipNone, skip)
	orderID := f.order(t)
	require.NoError(t, f.repo.AttachOrder(ctx, claim.Charge.ID, orderID))

	// A pending charge blocks; an old one does not.
	_, skip = f.claim(t)
	require.Equal(t, service.AutoTopUpSkipInFlight, skip)
	f.now = f.now.Add(16 * time.Minute)
	claim2, skip := f.claim(t)
	require.Equal(t, service.AutoTopUpSkipNone, skip)
	require.NotEqual(t, claim.Charge.ID, claim2.Charge.ID)

	// The first charge's order is paid after all (its answer was lost).
	charge, changed, err := f.repo.SettleByOrder(ctx, orderID, service.AutoTopUpChargeSucceeded, "", f.now)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, service.AutoTopUpChargeSucceeded, charge.Status)

	// Settling again changes nothing; an order that is not an automatic top-up is unknown.
	charge, changed, err = f.repo.SettleByOrder(ctx, orderID, service.AutoTopUpChargeSucceeded, "", f.now)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, service.AutoTopUpChargeSucceeded, charge.Status)
	charge, changed, err = f.repo.SettleByOrder(ctx, orderID+100000, service.AutoTopUpChargeSucceeded, "", f.now)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, charge)
}

func TestAutoTopUpRepoDeclinedChargeCannotBecomePaid(t *testing.T) {
	ctx := context.Background()
	f := newAutoTopUpRepoFixture(t, 1)
	claim, _ := f.claim(t)
	orderID := f.order(t)
	require.NoError(t, f.repo.AttachOrder(ctx, claim.Charge.ID, orderID))
	require.NoError(t, f.repo.FinishCharge(ctx, claim.Charge.ID, service.AutoTopUpChargeFailed, "insufficient_funds", f.now))

	charge, changed, err := f.repo.SettleByOrder(ctx, orderID, service.AutoTopUpChargeSucceeded, "", f.now)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, service.AutoTopUpChargeFailed, charge.Status)
}

func TestAutoTopUpRepoSettingsAndCard(t *testing.T) {
	ctx := context.Background()
	f := newAutoTopUpRepoFixture(t, 1)
	require.NoError(t, f.repo.Disable(ctx, f.userID, service.AutoTopUpReasonAuthenticationRequired, f.now))

	// Changing amounts while it stays off keeps the reason it was stopped; turning it on clears it.
	rec, err := f.repo.SaveSettings(ctx, f.userID, service.AutoTopUpSettings{Enabled: false, ThresholdUSD: 10, AmountUSD: 30, MonthlyCapUSD: 90})
	require.NoError(t, err)
	require.Equal(t, service.AutoTopUpReasonAuthenticationRequired, rec.DisabledReason)
	require.Equal(t, 30.0, rec.AmountUSD)
	rec, err = f.repo.SaveSettings(ctx, f.userID, service.AutoTopUpSettings{Enabled: true, ThresholdUSD: 10, AmountUSD: 30, MonthlyCapUSD: 90})
	require.NoError(t, err)
	require.Empty(t, rec.DisabledReason)
	require.Nil(t, rec.DisabledAt)
	require.True(t, rec.Enabled)

	// A customer of the same Stripe account keeps the card; one of another account drops it and turns it off.
	require.NoError(t, f.repo.SaveCustomer(ctx, f.userID, "7", "cus_1"))
	rec, _ = f.repo.Get(ctx, f.userID)
	require.Equal(t, "pm_1", rec.PaymentMethodID)
	require.True(t, rec.Enabled)
	require.NoError(t, f.repo.SaveCustomer(ctx, f.userID, "8", "cus_other"))
	rec, _ = f.repo.Get(ctx, f.userID)
	require.Empty(t, rec.PaymentMethodID)
	require.Empty(t, rec.CardLast4)
	require.False(t, rec.Enabled)
	require.Equal(t, "cus_other", rec.CustomerID)

	none, err := NewOtohaAutoTopUpRepository(integrationDB).Get(ctx, -1)
	require.NoError(t, err)
	require.Nil(t, none)
}

func TestAutoTopUpRepoSummary(t *testing.T) {
	ctx := context.Background()
	f := newAutoTopUpRepoFixture(t, 1)
	monthStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	sum, err := f.repo.Summary(ctx, f.userID, monthStart, monthStart.AddDate(0, 1, 0))
	require.NoError(t, err)
	require.Zero(t, sum.UsedUSD)
	require.Nil(t, sum.Last)

	claim, _ := f.claim(t)
	sum, _ = f.repo.Summary(ctx, f.userID, monthStart, monthStart.AddDate(0, 1, 0))
	require.Equal(t, 20.0, sum.UsedUSD, "a pending charge counts")
	require.Equal(t, service.AutoTopUpChargePending, sum.Last.Status)
	require.NoError(t, f.repo.FinishCharge(ctx, claim.Charge.ID, service.AutoTopUpChargeFailed, "card_declined", f.now))
	sum, _ = f.repo.Summary(ctx, f.userID, monthStart, monthStart.AddDate(0, 1, 0))
	require.Zero(t, sum.UsedUSD, "a failed charge does not")
	require.Equal(t, service.AutoTopUpChargeFailed, sum.Last.Status)
}
