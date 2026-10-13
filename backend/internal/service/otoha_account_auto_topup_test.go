//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeOtohaAutoTopUp struct {
	state *OtohaAutoTopUp
	err   error
}

func (f fakeOtohaAutoTopUp) State(context.Context, int64) (*OtohaAutoTopUp, error) {
	return f.state, f.err
}

// The app (OtohaAI) reads this: its shape is the contract for showing automatic top-up.
func TestOtohaAccountSummaryCarriesAutoTopUp(t *testing.T) {
	f := newOtohaFixture(t)
	last := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	f.svc.SetAutoTopUp(fakeOtohaAutoTopUp{state: &OtohaAutoTopUp{
		Enabled: true, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100, UsedThisMonthUSD: 40,
		Card:      &OtohaAutoTopUpCard{Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030},
		LastTopUp: &OtohaAutoTopUpLast{At: last, AmountUSD: 20, Result: "succeeded"},
		ManageURL: "https://account.otohaai.com/otoha/account#auto-topup",
	}})

	summary, err := f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	raw, err := json.Marshal(summary)
	require.NoError(t, err)

	var got struct {
		AutoTopUp map[string]any `json:"auto_topup"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, map[string]any{
		"enabled":             true,
		"threshold_usd":       5.0,
		"amount_usd":          20.0,
		"monthly_cap_usd":     100.0,
		"used_this_month_usd": 40.0,
		"card":                map[string]any{"brand": "visa", "last4": "4242", "exp_month": 12.0, "exp_year": 2030.0},
		"disabled_reason":     "",
		"disabled_at":         nil,
		"last_top_up":         map[string]any{"at": "2026-10-12T09:30:00Z", "amount_usd": 20.0, "result": "succeeded"},
		"manage_url":          "https://account.otohaai.com/otoha/account#auto-topup",
	}, got.AutoTopUp)
}

func TestOtohaAccountSummaryWithoutAutoTopUpOrWhenItCannotBeRead(t *testing.T) {
	f := newOtohaFixture(t)
	summary, err := f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.Nil(t, summary.AutoTopUp, "a server without automatic top-up says nothing about it")

	f.svc.SetAutoTopUp(fakeOtohaAutoTopUp{err: errors.New("database is down")})
	summary, err = f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err, "the account page still opens")
	require.Equal(t, 12.5, summary.Balance)
	require.Nil(t, summary.AutoTopUp)
}
