package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// SearchChargeQuote is a value snapshot: no account, credentials or mutable model pointers.
type SearchChargeQuote struct {
	UserID, APIKeyID, GroupID, SubscriptionID int64
	AmountUSD, UnitPriceUSD, RateMultiplier   float64
	BillingSource                             string
}

type SearchExecution struct {
	RequestID, Provider, PayloadHash                        string
	InboundEndpoint, UpstreamEndpoint, UserAgent, IPAddress string
	Duration                                                time.Duration
}

type SearchCostReceipt struct {
	AmountUSD      float64 `json:"amount_usd"`
	Currency       string  `json:"currency"`
	Calls          int     `json:"calls"`
	RequestID      string  `json:"request_id"`
	BillingSource  string  `json:"billing_source"`
	UnitPriceUSD   float64 `json:"unit_price_usd"`
	RateMultiplier float64 `json:"rate_multiplier"`
}

type SearchSettlementCommand struct {
	Quote     SearchChargeQuote
	Execution SearchExecution
}

// StandaloneSearchBillingRepository is an optional capability of UsageBillingRepository.
// The ledger is separate from usage_logs and is not included in existing usage reports.
// Search has no model-platform attribution: user-platform quota counters exclude
// these charges. Balance/subscription and API-key limits still apply.
type StandaloneSearchBillingRepository interface {
	SettleSearch(context.Context, *SearchSettlementCommand) (*SearchCostReceipt, error)
}

func (s *GatewayService) QuoteStandaloneSearch(ctx context.Context, key *APIKey, sub *UserSubscription) (*SearchChargeQuote, error) {
	if s == nil || s.billingService == nil {
		return nil, ErrBillingServiceUnavailable
	}
	if _, ok := s.usageBillingRepo.(StandaloneSearchBillingRepository); !ok {
		return nil, ErrBillingServiceUnavailable
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil, ErrBillingServiceUnavailable
	}
	if key == nil || key.User == nil || key.Group == nil || key.GroupID == nil || key.ID <= 0 || key.User.ID <= 0 || key.UserID != key.User.ID || *key.GroupID != key.Group.ID || key.Group.ID <= 0 {
		return nil, errors.New("invalid standalone search billing identity")
	}
	price := defaultWebSearchPricePerCall
	if key.Group.WebSearchPricePerCall != nil {
		price = *key.Group.WebSearchPricePerCall
	}
	rate := s.ResolveUserGroupRateMultiplier(ctx, key.User.ID, key.Group.ID, key.Group.RateMultiplier)
	if !searchAmountValid(price) || !searchAmountValid(rate) {
		return nil, errors.New("invalid standalone search pricing")
	}
	cost := s.billingService.CalculateWebSearchCost(1, &price, rate)
	q := &SearchChargeQuote{UserID: key.User.ID, APIKeyID: key.ID, GroupID: key.Group.ID, AmountUSD: QuantizeUsageBillingAmount(cost.ActualCost), UnitPriceUSD: price, RateMultiplier: rate, BillingSource: "balance"}
	if key.Group.IsSubscriptionType() {
		if sub == nil || sub.ID <= 0 || sub.UserID != q.UserID || sub.GroupID != q.GroupID {
			return nil, ErrSubscriptionNotFound
		}
		if sub.Status != SubscriptionStatusActive {
			return nil, ErrSubscriptionSuspended
		}
		if now := time.Now(); now.Before(sub.StartsAt) || !now.Before(sub.ExpiresAt) {
			return nil, ErrSubscriptionExpired
		}
		q.SubscriptionID, q.BillingSource = sub.ID, "subscription"
	}
	if !searchAmountValid(q.AmountUSD) {
		return nil, errors.New("invalid standalone search amount")
	}
	return q, nil
}

func searchAmountValid(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (c *SearchSettlementCommand) Validate() error {
	if c == nil {
		return errors.New("missing standalone search settlement")
	}
	q, e := c.Quote, c.Execution
	if q.UserID <= 0 || q.APIKeyID <= 0 || q.GroupID <= 0 || !searchAmountValid(q.AmountUSD) || !searchAmountValid(q.UnitPriceUSD) || !searchAmountValid(q.RateMultiplier) || q.AmountUSD != QuantizeUsageBillingAmount(q.UnitPriceUSD*q.RateMultiplier) {
		return errors.New("invalid standalone search quote")
	}
	if (q.BillingSource != "balance" || q.SubscriptionID != 0) && (q.BillingSource != "subscription" || q.SubscriptionID <= 0) {
		return errors.New("invalid standalone search billing source")
	}
	if strings.TrimSpace(e.RequestID) == "" || len(e.RequestID) > 128 || strings.TrimSpace(e.Provider) == "" || len(e.Provider) > 128 || strings.TrimSpace(e.PayloadHash) == "" || len(e.PayloadHash) > 128 || e.Duration < 0 {
		return errors.New("invalid standalone search execution")
	}
	return nil
}

func (s *GatewayService) SettleStandaloneSearch(ctx context.Context, quote *SearchChargeQuote, execution SearchExecution) (*SearchCostReceipt, error) {
	if s == nil || quote == nil {
		return nil, ErrBillingServiceUnavailable
	}
	repo, ok := s.usageBillingRepo.(StandaloneSearchBillingRepository)
	if !ok {
		return nil, ErrBillingServiceUnavailable
	}
	cmd := &SearchSettlementCommand{Quote: *quote, Execution: execution}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := detachedBillingContext(ctx)
	defer cancel()
	receipt, err := repo.SettleSearch(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, ErrBillingServiceUnavailable
	}
	// Invalidation is synchronous and replay-safe; never decrement twice on retry.
	// A committed receipt remains successful even if cache refresh fails. Strict DB
	// guards protect subsequent settlements. The caller owns reservation release.
	if cache := s.billingCacheService; cache != nil {
		var ownerErr error
		if quote.BillingSource == "subscription" {
			ownerErr = cache.InvalidateSubscription(ctx, quote.UserID, quote.GroupID)
		} else {
			ownerErr = cache.InvalidateUserBalance(ctx, quote.UserID)
		}
		keyErr := cache.InvalidateAPIKeyRateLimit(ctx, quote.APIKeyID)
		if err := errors.Join(ownerErr, keyErr); err != nil {
			slog.Warn("standalone search committed; billing cache refresh failed", "request_id", execution.RequestID, "api_key_id", quote.APIKeyID, "error", err)
		}
	}
	return receipt, nil
}

// PrepareSearchSubscriptionCharge applies existing window rules to a locked DB
// snapshot. The repository persists the result in the settlement transaction.
func PrepareSearchSubscriptionCharge(sub *UserSubscription, group *Group, amount float64, now time.Time) error {
	if sub == nil || group == nil || !searchAmountValid(amount) {
		return ErrSubscriptionNotFound
	}
	if sub.Status != SubscriptionStatusActive {
		return ErrSubscriptionSuspended
	}
	if now.Before(sub.StartsAt) || !now.Before(sub.ExpiresAt) {
		return ErrSubscriptionExpired
	}
	if sub.DailyWindowStart == nil {
		start := timezone.StartOfDay(now)
		sub.DailyWindowStart = &start
		sub.DailyUsageUSD = 0
	} else if start, reset := sub.automaticDailyWindowStartAt(now); reset {
		sub.DailyWindowStart = &start
		sub.DailyUsageUSD = 0
	}
	for _, w := range []struct {
		start  **time.Time
		usage  *float64
		period time.Duration
	}{{&sub.WeeklyWindowStart, &sub.WeeklyUsageUSD, 7 * 24 * time.Hour}, {&sub.MonthlyWindowStart, &sub.MonthlyUsageUSD, 30 * 24 * time.Hour}} {
		if *w.start == nil {
			start := now
			*w.start = &start
			*w.usage = 0
		} else if start, reset := sub.automaticWindowStartAt(*w.start, w.period, now); reset {
			*w.start = &start
			*w.usage = 0
		}
	}
	for _, limit := range []struct {
		usage *float64
		cap   *float64
		err   error
	}{{&sub.DailyUsageUSD, group.DailyLimitUSD, ErrDailyLimitExceeded}, {&sub.WeeklyUsageUSD, group.WeeklyLimitUSD, ErrWeeklyLimitExceeded}, {&sub.MonthlyUsageUSD, group.MonthlyLimitUSD, ErrMonthlyLimitExceeded}} {
		next := QuantizeUsageBillingAmount(*limit.usage + amount)
		if limit.cap != nil && *limit.cap > 0 && next > *limit.cap {
			return limit.err
		}
		*limit.usage = next
	}
	return nil
}
