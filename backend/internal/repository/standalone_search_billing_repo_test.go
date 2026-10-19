//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestStandaloneSearchRepositoryRejectsInvalidCommand(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &usageBillingRepository{db: db}
	_, err = r.SettleSearch(context.Background(), nil)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectSearchClaim(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO usage_billing_dedup").WithArgs("standalone-search:search:1", int64(2), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT request_fingerprint.*usage_billing_dedup_archive").WillReturnError(sql.ErrNoRows)
}

func expectSearchGroup(mock sqlmock.Sqlmock, subscription bool) {
	expectSearchGroupFallback(mock, subscription, false)
}

func expectSearchGroupFallback(mock sqlmock.Sqlmock, subscription, fallback bool) {
	kind := "standard"
	if subscription {
		kind = "subscription"
	}
	now := time.Now()
	mock.ExpectQuery("SELECT subscription_type.*date_trunc\\('day', NOW\\(\\)\\).*FOR SHARE").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"kind", "daily", "weekly", "monthly", "fallback", "now", "day"}).AddRow(kind, 1.0, 2.0, 3.0, fallback, now, now.Truncate(24*time.Hour)))
}

func expectSearchKey(mock sqlmock.Sqlmock, quota, used float64) {
	now := time.Now()
	mock.ExpectQuery("SELECT status, expires_at.*FOR UPDATE").WithArgs(int64(2), int64(1), int64(3)).WillReturnRows(sqlmock.NewRows([]string{"status", "expires", "quota", "used", "l5", "l1", "l7", "u5", "u1", "u7", "w5", "w1", "w7"}).AddRow("active", nil, quota, used, 0, 0, 0, 0, 0, 0, now, now, now))
}

func expectSearchReceipt(mock sqlmock.Sqlmock, source string) {
	mock.ExpectQuery("SELECT amount_usd.*FROM standalone_search_usage").WithArgs(int64(2), "search:1", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"amount", "currency", "calls", "request", "source", "unit", "rate"}).AddRow(.01, "USD", 1, "search:1", source, .01, 1))
}

func TestStandaloneSearchRepositoryBalanceAtomic(t *testing.T) {
	for _, failure := range []string{"", "balance", "ledger", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			expectSearchClaim(mock)
			expectSearchGroup(mock, false)
			balance := mock.ExpectQuery("UPDATE users SET balance.*balance >= \\$1 RETURNING balance").WithArgs(.01, int64(1))
			if failure == "balance" {
				balance.WillReturnError(sql.ErrNoRows)
				mock.ExpectRollback()
			} else {
				balance.WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(.99))
				expectSearchKey(mock, 1, 0)
				mock.ExpectExec("UPDATE api_keys SET quota_used").WillReturnResult(sqlmock.NewResult(0, 1))
				insert := mock.ExpectExec("INSERT INTO standalone_search_usage")
				if failure == "ledger" {
					insert.WillReturnError(errors.New("ledger unavailable"))
					mock.ExpectRollback()
				} else {
					insert.WillReturnResult(sqlmock.NewResult(1, 1))
					expectSearchReceipt(mock, "balance")
					commit := mock.ExpectCommit()
					if failure == "commit" {
						commit.WillReturnError(errors.New("commit failed"))
					}
				}
			}
			receipt, err := (&usageBillingRepository{db: db}).SettleSearch(context.Background(), searchRepoCommand())
			if failure == "" {
				require.NoError(t, err)
				require.Equal(t, .01, receipt.AmountUSD)
			} else {
				require.Error(t, err)
				require.Nil(t, receipt)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStandaloneSearchRepositoryOwnerBeforeKeyCapRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectSearchClaim(mock)
	expectSearchGroup(mock, false)
	mock.ExpectQuery("UPDATE users SET balance").WithArgs(.01, int64(1)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(.99))
	expectSearchKey(mock, 1, .995)
	mock.ExpectRollback()
	_, err = (&usageBillingRepository{db: db}).SettleSearch(context.Background(), searchRepoCommand())
	require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStandaloneSearchRepositorySubscription(t *testing.T) {
	for _, used := range []float64{.99, 1} {
		t.Run("subscription", func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			cmd := searchRepoCommand()
			cmd.Quote.BillingSource = "subscription"
			cmd.Quote.SubscriptionID = 4
			expectSearchClaim(mock)
			expectSearchGroup(mock, true)
			mock.ExpectQuery("SELECT id FROM users").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			now := time.Now()
			mock.ExpectQuery("SELECT status, starts_at.*FOR UPDATE").WithArgs(int64(4), int64(1), int64(3)).WillReturnRows(sqlmock.NewRows([]string{"status", "start", "expires", "d", "w", "m", "du", "wu", "mu", "dl", "wl", "ml"}).AddRow("active", now.Add(-time.Hour), now.Add(48*time.Hour), now, now, now, used, 0, 0, nil, nil, nil))
			if used == 1 {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE user_subscriptions").WillReturnResult(sqlmock.NewResult(0, 1))
				expectSearchKey(mock, 0, 0)
				mock.ExpectExec("UPDATE api_keys").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("INSERT INTO standalone_search_usage").WillReturnResult(sqlmock.NewResult(1, 1))
				expectSearchReceipt(mock, "subscription")
				mock.ExpectCommit()
			}
			receipt, err := (&usageBillingRepository{db: db}).SettleSearch(context.Background(), cmd)
			if used == 1 {
				require.ErrorIs(t, err, service.ErrDailyLimitExceeded)
			} else {
				require.NoError(t, err)
				require.Equal(t, "subscription", receipt.BillingSource)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStandaloneSearchKeyWindows(t *testing.T) {
	now := time.Now()
	expired := now.Add(-25 * time.Hour)
	key := service.APIKey{Status: "active", RateLimit1d: 1, Usage1d: 1, Window1dStart: &expired}
	require.NoError(t, prepareSearchKeyCharge(&key, .01, now, now.Truncate(24*time.Hour)))
	require.Equal(t, .01, key.Usage1d)
	key.Usage1d = 1
	require.ErrorIs(t, prepareSearchKeyCharge(&key, .01, now, now.Truncate(24*time.Hour)), service.ErrAPIKeyRateLimit1dExceeded)
}

func TestStandaloneSearchKeyWindowMiddayAnchors(t *testing.T) {
	now := time.Date(2026, 10, 18, 13, 45, 0, 0, time.UTC)
	// Simulate a DB session timezone different from the Go timestamp's location.
	day := time.Date(2026, 10, 18, 0, 0, 0, 0, time.FixedZone("database", 9*60*60))
	for _, initialized := range []bool{false, true} {
		key := service.APIKey{Status: "active"}
		if initialized {
			old := now.Add(-8 * 24 * time.Hour)
			key.Window5hStart, key.Window1dStart, key.Window7dStart = &old, &old, &old
		}
		require.NoError(t, prepareSearchKeyCharge(&key, .01, now, day))
		require.Equal(t, now, *key.Window5hStart)
		require.Equal(t, day, *key.Window1dStart)
		require.Equal(t, day, *key.Window7dStart)
	}
}

func TestStandaloneSearchRepositoryReplayAndConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run("replay", func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			cmd := searchRepoCommand()
			payload, err := json.Marshal(struct {
				Quote                                                    service.SearchChargeQuote
				Provider, PayloadHash, InboundEndpoint, UpstreamEndpoint string
			}{cmd.Quote, "test", "hash", "", ""})
			require.NoError(t, err)
			sum := sha256.Sum256(payload)
			fingerprint := hex.EncodeToString(sum[:])
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO usage_billing_dedup").WithArgs("standalone-search:search:1", int64(2), fingerprint).WillReturnError(sql.ErrNoRows)
			stored := fingerprint
			if conflict {
				stored = "different"
			}
			mock.ExpectQuery("SELECT request_fingerprint.*FROM usage_billing_dedup").WillReturnRows(sqlmock.NewRows([]string{"fingerprint"}).AddRow(stored))
			if conflict {
				mock.ExpectRollback()
			} else {
				expectSearchReceipt(mock, "balance")
				mock.ExpectCommit()
			}
			r, err := (&usageBillingRepository{db: db}).SettleSearch(context.Background(), cmd)
			if conflict {
				require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
			} else {
				require.NoError(t, err)
				require.Equal(t, .01, r.AmountUSD)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func searchRepoCommand() *service.SearchSettlementCommand {
	return &service.SearchSettlementCommand{Quote: service.SearchChargeQuote{UserID: 1, APIKeyID: 2, GroupID: 3, AmountUSD: .01, UnitPriceUSD: .01, RateMultiplier: 1, BillingSource: "balance"}, Execution: service.SearchExecution{RequestID: "search:1", Provider: "test", PayloadHash: "hash"}}
}

func TestStandaloneSearchRepositoryBalanceFallback(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			expectSearchClaim(mock)
			expectSearchGroupFallback(mock, true, enabled)
			if enabled {
				mock.ExpectQuery("UPDATE users SET balance").WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(.99))
				expectSearchKey(mock, 0, 0)
				mock.ExpectExec("UPDATE api_keys").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("INSERT INTO standalone_search_usage").WillReturnResult(sqlmock.NewResult(1, 1))
				expectSearchReceipt(mock, "balance")
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			receipt, err := (&usageBillingRepository{db: db}).SettleSearch(context.Background(), searchRepoCommand())
			if enabled {
				require.NoError(t, err)
				require.Equal(t, "balance", receipt.BillingSource)
			} else {
				require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStandaloneSearchRepositoryOwnPlanLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits [3]any
		used   [3]float64
		want   error
	}{
		{"higher", [3]any{2., 3., 4.}, [3]float64{1., 2., 3.}, nil},
		{"daily", [3]any{.5, nil, nil}, [3]float64{.5, 0, 0}, service.ErrDailyLimitExceeded},
		{"weekly", [3]any{nil, .5, nil}, [3]float64{0, .5, 0}, service.ErrWeeklyLimitExceeded},
		{"monthly", [3]any{nil, nil, .5}, [3]float64{0, 0, .5}, service.ErrMonthlyLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cmd := searchRepoCommand()
			cmd.Quote.BillingSource = "subscription"
			cmd.Quote.SubscriptionID = 4
			expectSearchClaim(mock)
			expectSearchGroup(mock, true)
			mock.ExpectQuery("SELECT id FROM users").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			now := time.Now()
			mock.ExpectQuery("SELECT status, starts_at.*daily_limit_usd, weekly_limit_usd, monthly_limit_usd.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"status", "s", "e", "d", "w", "m", "du", "wu", "mu", "dl", "wl", "ml"}).AddRow("active", now.Add(-time.Hour), now.Add(time.Hour), now, now, now, tc.used[0], tc.used[1], tc.used[2], tc.limits[0], tc.limits[1], tc.limits[2]))
			if tc.want != nil {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE user_subscriptions").WithArgs(1.01, 2.01, 3.01, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
				expectSearchKey(mock, 0, 0)
				mock.ExpectExec("UPDATE api_keys").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("INSERT INTO standalone_search_usage").WillReturnResult(sqlmock.NewResult(1, 1))
				expectSearchReceipt(mock, "subscription")
				mock.ExpectCommit()
			}
			_, err = (&usageBillingRepository{db: db}).SettleSearch(context.Background(), cmd)
			require.ErrorIs(t, err, tc.want)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
