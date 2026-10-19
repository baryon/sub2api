//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func standaloneSearchFixture(t *testing.T, subscription bool) (*usageBillingRepository, *service.SearchSettlementCommand) {
	t.Helper()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: "search-" + uuid.NewString() + "@example.com", Balance: .01})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Audit rows deliberately restrict parent deletion. Remove this fixture's
		// ledger before downstream suites clean up users, keys and subscriptions.
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM standalone_search_usage WHERE user_id = $1`, user.ID)
		require.NoError(t, err, "clean up standalone search fixture ledger")
	})
	group := &service.Group{Name: "search-" + uuid.NewString(), RateMultiplier: 1}
	if subscription {
		limit := .01
		group.SubscriptionType = service.SubscriptionTypeSubscription
		group.DailyLimitUSD = &limit
	}
	group = mustCreateGroup(t, client, group)
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "test-search-" + uuid.NewString()})
	q := service.SearchChargeQuote{UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, AmountUSD: .01, UnitPriceUSD: .01, RateMultiplier: 1, BillingSource: "balance"}
	if subscription {
		sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: user.ID, GroupID: group.ID})
		q.SubscriptionID = sub.ID
		q.BillingSource = "subscription"
	}
	cmd := &service.SearchSettlementCommand{Quote: q, Execution: service.SearchExecution{RequestID: uuid.NewString(), Provider: "integration", PayloadHash: "payload", InboundEndpoint: "/tools/search", UpstreamEndpoint: "/search", Duration: time.Millisecond}}
	return &usageBillingRepository{db: integrationDB}, cmd
}

func searchLedgerCount(t *testing.T, userID int64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM standalone_search_usage WHERE user_id = $1`, userID).Scan(&count))
	return count
}

func TestStandaloneSearchIntegrationSchemaAndReplay(t *testing.T) {
	r, cmd := standaloneSearchFixture(t, false)
	ctx := context.Background()
	first, err := r.SettleSearch(ctx, cmd)
	require.NoError(t, err)
	second, err := r.SettleSearch(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, .01, first.AmountUSD)
	require.Equal(t, "USD", first.Currency)
	require.Equal(t, 1, searchLedgerCount(t, cmd.Quote.UserID))
	var balance, quota float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.Quote.UserID).Scan(&balance))
	require.Zero(t, balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, cmd.Quote.APIKeyID).Scan(&quota))
	require.Equal(t, .01, quota)
	var accountColumns int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='standalone_search_usage' AND column_name='account_id'`).Scan(&accountColumns))
	require.Zero(t, accountColumns)
	cmd.Execution.PayloadHash = "different"
	_, err = r.SettleSearch(ctx, cmd)
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
}

func TestStandaloneSearchIntegrationLedgerFailureRollsBack(t *testing.T) {
	r, cmd := standaloneSearchFixture(t, false)
	// PostgreSQL TEXT rejects NUL at the ledger INSERT, after balance/key writes.
	cmd.Execution.UserAgent = "invalid\x00text"
	receipt, err := r.SettleSearch(context.Background(), cmd)
	require.Error(t, err)
	require.Nil(t, receipt)
	require.Zero(t, searchLedgerCount(t, cmd.Quote.UserID))
	var balance, quota float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.Quote.UserID).Scan(&balance))
	require.Equal(t, .01, balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, cmd.Quote.APIKeyID).Scan(&quota))
	require.Zero(t, quota)
	var claims int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id=$1 AND request_id=$2`, cmd.Quote.APIKeyID, "standalone-search:"+cmd.Execution.RequestID).Scan(&claims))
	require.Zero(t, claims)
	cmd.Execution.UserAgent = "valid"
	_, err = r.SettleSearch(context.Background(), cmd)
	require.NoError(t, err)
}

func TestStandaloneSearchIntegrationConcurrentCaps(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		t.Run(fmt.Sprint("subscription=", subscription), func(t *testing.T) {
			r, first := standaloneSearchFixture(t, subscription)
			second := *first
			second.Execution.RequestID = uuid.NewString()
			// Different keys ensure serialization is on the shared balance/subscription,
			// not merely on a single API-key row.
			key := mustCreateApiKey(t, testEntClient(t), &service.APIKey{UserID: first.Quote.UserID, GroupID: &first.Quote.GroupID, Key: "test-search-" + uuid.NewString()})
			second.Quote.APIKeyID = key.ID
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, cmd := range []*service.SearchSettlementCommand{first, &second} {
				go func(c *service.SearchSettlementCommand) { <-start; _, err := r.SettleSearch(ctx, c); results <- err }(cmd)
			}
			close(start)
			success := 0
			for range 2 {
				err := <-results
				if err == nil {
					success++
				} else if subscription {
					require.ErrorIs(t, err, service.ErrDailyLimitExceeded)
				} else {
					require.ErrorIs(t, err, service.ErrInsufficientBalance)
				}
			}
			require.Equal(t, 1, success)
			require.Equal(t, 1, searchLedgerCount(t, first.Quote.UserID))
			var balance float64
			require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, first.Quote.UserID).Scan(&balance))
			if subscription {
				require.Equal(t, .01, balance)
				var daily, weekly, monthly float64
				require.NoError(t, integrationDB.QueryRow(`SELECT daily_usage_usd, weekly_usage_usd, monthly_usage_usd FROM user_subscriptions WHERE id=$1`, first.Quote.SubscriptionID).Scan(&daily, &weekly, &monthly))
				require.Equal(t, .01, daily)
				require.Equal(t, daily, weekly)
				require.Equal(t, daily, monthly)
			} else {
				require.Zero(t, balance)
			}
		})
	}
}

func TestStandaloneSearchIntegrationBalanceFallback(t *testing.T) {
	r, cmd := standaloneSearchFixture(t, true)
	// Middleware selected balance because no eligible subscription remained.
	cmd.Quote.SubscriptionID = 0
	cmd.Quote.BillingSource = "balance"
	_, err := integrationDB.Exec(`UPDATE groups SET balance_fallback_enabled = TRUE WHERE id = $1`, cmd.Quote.GroupID)
	require.NoError(t, err)
	receipt, err := r.SettleSearch(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, "balance", receipt.BillingSource)
	var balance, used float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.Quote.UserID).Scan(&balance))
	require.Zero(t, balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE user_id=$1 AND group_id=$2`, cmd.Quote.UserID, cmd.Quote.GroupID).Scan(&used))
	require.Zero(t, used)
}

func TestStandaloneSearchIntegrationOwnPlanLimits(t *testing.T) {
	for _, tc := range []struct {
		name, column    string
		allowance, used float64
		want            error
	}{
		{"higher", "daily", .03, .01, nil},
		{"daily", "daily", .005, 0, service.ErrDailyLimitExceeded},
		{"weekly", "weekly", .005, 0, service.ErrWeeklyLimitExceeded},
		{"monthly", "monthly", .005, 0, service.ErrMonthlyLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, cmd := standaloneSearchFixture(t, true)
			// Column comes only from the fixed test cases above.
			_, err := integrationDB.Exec(`UPDATE user_subscriptions SET `+tc.column+`_limit_usd=$1, `+tc.column+`_usage_usd=$2, daily_window_start=NOW(), weekly_window_start=NOW(), monthly_window_start=NOW() WHERE id=$3`, tc.allowance, tc.used, cmd.Quote.SubscriptionID)
			require.NoError(t, err)
			_, err = r.SettleSearch(context.Background(), cmd)
			require.ErrorIs(t, err, tc.want)
			if tc.want == nil {
				require.Equal(t, 1, searchLedgerCount(t, cmd.Quote.UserID))
			} else {
				require.Zero(t, searchLedgerCount(t, cmd.Quote.UserID))
			}
		})
	}
}
