//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingAutoTopUp struct {
	calls []struct {
		userID  int64
		balance float64
	}
}

func (r *recordingAutoTopUp) OnBalanceDeducted(_ context.Context, userID int64, newBalance float64) {
	r.calls = append(r.calls, struct {
		userID  int64
		balance float64
	}{userID, newBalance})
}

// Every balance deduction reaches automatic top-up with the balance that is left, even when the user has turned
// the low-balance email off.
func TestCheckBalanceAfterDeductionTellsAutoTopUpTheBalanceLeft(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	rec := &recordingAutoTopUp{}
	s.SetAutoTopUp(rec)

	s.CheckBalanceAfterDeduction(context.Background(), &User{ID: 9, BalanceNotifyEnabled: false}, 6.5, 2.0)

	require.Len(t, rec.calls, 1)
	require.Equal(t, int64(9), rec.calls[0].userID)
	require.InDelta(t, 4.5, rec.calls[0].balance, 1e-9)
}

func TestCheckBalanceAfterDeductionIgnoresFreeRequestsAndMissingUsers(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	rec := &recordingAutoTopUp{}
	s.SetAutoTopUp(rec)

	s.CheckBalanceAfterDeduction(context.Background(), &User{ID: 9}, 6.5, 0)
	s.CheckBalanceAfterDeduction(context.Background(), nil, 6.5, 1)
	require.Empty(t, rec.calls)
}
