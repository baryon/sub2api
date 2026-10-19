package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.StandaloneSearchBillingRepository = (*usageBillingRepository)(nil)

func (r *usageBillingRepository) SettleSearch(ctx context.Context, cmd *service.SearchSettlementCommand) (*service.SearchCostReceipt, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, service.ErrBillingServiceUnavailable
	}
	q, e := cmd.Quote, cmd.Execution
	payload, err := json.Marshal(struct {
		Quote                                                    service.SearchChargeQuote
		Provider, PayloadHash, InboundEndpoint, UpstreamEndpoint string
	}{q, e.Provider, e.PayloadHash, e.InboundEndpoint, e.UpstreamEndpoint})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(hash[:])
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	applied, err := r.claimUsageBillingRequest(ctx, tx, "standalone-search:"+e.RequestID, q.APIKeyID, fingerprint)
	if err != nil {
		return nil, err
	}
	if !applied {
		receipt, err := readSearchReceipt(ctx, tx, q.APIKeyID, e.RequestID, fingerprint)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return receipt, nil
	}
	var now, databaseDay time.Time
	var group service.Group
	group.ID = q.GroupID
	err = tx.QueryRowContext(ctx, `SELECT subscription_type, daily_limit_usd, weekly_limit_usd, monthly_limit_usd, balance_fallback_enabled, NOW(), date_trunc('day', NOW())
		FROM groups WHERE id = $1 AND deleted_at IS NULL AND status = 'active' FOR SHARE`, q.GroupID).Scan(&group.SubscriptionType, &group.DailyLimitUSD, &group.WeeklyLimitUSD, &group.MonthlyLimitUSD, &group.BalanceFallbackEnabled, &now, &databaseDay)
	if err != nil {
		return nil, err
	}
	if (q.BillingSource == "subscription" && !group.IsSubscriptionType()) ||
		(q.BillingSource == "balance" && group.IsSubscriptionType() && !group.BalanceFallbackEnabled) {
		return nil, service.ErrUsageBillingRequestConflict
	}
	// Match model usage settlement: lock the balance/subscription before the key.
	// Any subsequent key validation failure rolls the owner update back.
	if q.BillingSource == "balance" {
		var balance float64
		err = tx.QueryRowContext(ctx, `UPDATE users SET balance = balance - $1, updated_at = NOW()
			WHERE id = $2 AND deleted_at IS NULL AND status = 'active' AND balance >= $1 RETURNING balance`, q.AmountUSD, q.UserID).Scan(&balance)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrInsufficientBalance
		}
		if err != nil {
			return nil, err
		}
	} else {
		var userID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL AND status = 'active' FOR SHARE`, q.UserID).Scan(&userID); err != nil {
			return nil, err
		}
		sub := service.UserSubscription{ID: q.SubscriptionID, UserID: q.UserID, GroupID: q.GroupID}
		err = tx.QueryRowContext(ctx, `SELECT status, starts_at, expires_at, daily_window_start, weekly_window_start, monthly_window_start,
			daily_usage_usd, weekly_usage_usd, monthly_usage_usd, daily_limit_usd, weekly_limit_usd, monthly_limit_usd FROM user_subscriptions
			WHERE id = $1 AND user_id = $2 AND group_id = $3 AND deleted_at IS NULL FOR UPDATE`, q.SubscriptionID, q.UserID, q.GroupID).Scan(
			&sub.Status, &sub.StartsAt, &sub.ExpiresAt, &sub.DailyWindowStart, &sub.WeeklyWindowStart, &sub.MonthlyWindowStart, &sub.DailyUsageUSD, &sub.WeeklyUsageUSD, &sub.MonthlyUsageUSD, &sub.DailyLimitUSD, &sub.WeeklyLimitUSD, &sub.MonthlyLimitUSD)
		if err != nil {
			return nil, err
		}
		if err := service.PrepareSearchSubscriptionCharge(&sub, &group, q.AmountUSD, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE user_subscriptions SET daily_usage_usd = $1, weekly_usage_usd = $2, monthly_usage_usd = $3,
			daily_window_start = $4, weekly_window_start = $5, monthly_window_start = $6, updated_at = NOW() WHERE id = $7`,
			sub.DailyUsageUSD, sub.WeeklyUsageUSD, sub.MonthlyUsageUSD, sub.DailyWindowStart, sub.WeeklyWindowStart, sub.MonthlyWindowStart, sub.ID); err != nil {
			return nil, err
		}
	}
	var key service.APIKey
	err = tx.QueryRowContext(ctx, `SELECT status, expires_at, quota, quota_used, rate_limit_5h, rate_limit_1d, rate_limit_7d,
		usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start
		FROM api_keys WHERE id = $1 AND user_id = $2 AND group_id = $3 AND deleted_at IS NULL FOR UPDATE`, q.APIKeyID, q.UserID, q.GroupID).Scan(
		&key.Status, &key.ExpiresAt, &key.Quota, &key.QuotaUsed, &key.RateLimit5h, &key.RateLimit1d, &key.RateLimit7d,
		&key.Usage5h, &key.Usage1d, &key.Usage7d, &key.Window5hStart, &key.Window1dStart, &key.Window7dStart)
	if err != nil {
		return nil, err
	}
	if err := prepareSearchKeyCharge(&key, q.AmountUSD, now, databaseDay); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET quota_used = $1, usage_5h = $2, usage_1d = $3, usage_7d = $4,
		window_5h_start = $5, window_1d_start = $6, window_7d_start = $7, status = $8, updated_at = NOW() WHERE id = $9`,
		key.QuotaUsed, key.Usage5h, key.Usage1d, key.Usage7d, key.Window5hStart, key.Window1dStart, key.Window7dStart, key.Status, q.APIKeyID); err != nil {
		return nil, err
	}
	var subscriptionID any
	if q.SubscriptionID > 0 {
		subscriptionID = q.SubscriptionID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO standalone_search_usage
		(request_id, request_fingerprint, user_id, api_key_id, group_id, subscription_id, provider, payload_hash,
		amount_usd, unit_price_usd, rate_multiplier, billing_source, inbound_endpoint, upstream_endpoint, user_agent, ip_address, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		e.RequestID, fingerprint, q.UserID, q.APIKeyID, q.GroupID, subscriptionID, e.Provider, e.PayloadHash, q.AmountUSD, q.UnitPriceUSD, q.RateMultiplier, q.BillingSource, e.InboundEndpoint, e.UpstreamEndpoint, e.UserAgent, e.IPAddress, e.Duration.Milliseconds())
	if err != nil {
		return nil, err
	}
	// Read numeric values back so the receipt exactly reflects persisted precision.
	receipt, err := readSearchReceipt(ctx, tx, q.APIKeyID, e.RequestID, fingerprint)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return receipt, nil
}

func readSearchReceipt(ctx context.Context, tx *sql.Tx, keyID int64, requestID, fingerprint string) (*service.SearchCostReceipt, error) {
	r := &service.SearchCostReceipt{}
	err := tx.QueryRowContext(ctx, `SELECT amount_usd, currency, calls, request_id, billing_source, unit_price_usd, rate_multiplier
		FROM standalone_search_usage WHERE api_key_id = $1 AND request_id = $2 AND request_fingerprint = $3`, keyID, requestID, fingerprint).Scan(
		&r.AmountUSD, &r.Currency, &r.Calls, &r.RequestID, &r.BillingSource, &r.UnitPriceUSD, &r.RateMultiplier)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func prepareSearchKeyCharge(key *service.APIKey, amount float64, now, databaseDay time.Time) error {
	if key.Status == service.StatusAPIKeyQuotaExhausted {
		return service.ErrAPIKeyQuotaExhausted
	}
	if key.Status != service.StatusAPIKeyActive {
		return service.ErrAPIKeyNotFound
	}
	if key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) {
		return service.ErrAPIKeyExpired
	}
	next := service.QuantizeUsageBillingAmount(key.QuotaUsed + amount)
	if key.Quota > 0 && next > key.Quota {
		return service.ErrAPIKeyQuotaExhausted
	}
	key.QuotaUsed = next
	if key.Quota > 0 && next >= key.Quota {
		key.Status = service.StatusAPIKeyQuotaExhausted
	}
	for _, window := range []struct {
		start    **time.Time
		usage    *float64
		limit    float64
		duration time.Duration
		anchor   time.Time
		err      error
	}{
		{&key.Window5hStart, &key.Usage5h, key.RateLimit5h, 5 * time.Hour, now, service.ErrAPIKeyRateLimit5hExceeded},
		{&key.Window1dStart, &key.Usage1d, key.RateLimit1d, 24 * time.Hour, databaseDay, service.ErrAPIKeyRateLimit1dExceeded},
		{&key.Window7dStart, &key.Usage7d, key.RateLimit7d, 7 * 24 * time.Hour, databaseDay, service.ErrAPIKeyRateLimit7dExceeded},
	} {
		if *window.start == nil || !now.Before((*window.start).Add(window.duration)) {
			start := window.anchor
			*window.start = &start
			*window.usage = 0
		}
		next := service.QuantizeUsageBillingAmount(*window.usage + amount)
		if window.limit > 0 && next > window.limit {
			return window.err
		}
		*window.usage = next
	}
	return nil
}
