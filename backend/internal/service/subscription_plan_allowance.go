package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// SubscriptionLimits are the daily, weekly and monthly limits (USD) that apply to one subscription. A nil or
// non-positive value means no limit for that window, as for groups.
type SubscriptionLimits struct {
	DailyUSD   *float64
	WeeklyUSD  *float64
	MonthlyUSD *float64
}

func (l SubscriptionLimits) HasDaily() bool   { return positiveLimit(l.DailyUSD) }
func (l SubscriptionLimits) HasWeekly() bool  { return positiveLimit(l.WeeklyUSD) }
func (l SubscriptionLimits) HasMonthly() bool { return positiveLimit(l.MonthlyUSD) }

func positiveLimit(v *float64) bool { return v != nil && *v > 0 }

// EffectiveLimits returns the limits that apply to this subscription: per window, the allowance recorded from
// its plan when there is one, otherwise the group's value exactly as the group has it (TASK-57). A subscription
// without an allowance of its own therefore behaves exactly as before plans had allowances.
func (s *UserSubscription) EffectiveLimits(group *Group) SubscriptionLimits {
	var limits SubscriptionLimits
	if group != nil {
		limits = SubscriptionLimits{DailyUSD: group.DailyLimitUSD, WeeklyUSD: group.WeeklyLimitUSD, MonthlyUSD: group.MonthlyLimitUSD}
	}
	if s == nil {
		return limits
	}
	if positiveLimit(s.DailyLimitUSD) {
		limits.DailyUSD = s.DailyLimitUSD
	}
	if positiveLimit(s.WeeklyLimitUSD) {
		limits.WeeklyUSD = s.WeeklyLimitUSD
	}
	if positiveLimit(s.MonthlyLimitUSD) {
		limits.MonthlyUSD = s.MonthlyLimitUSD
	}
	return limits
}

// subscriptionPlanTerms is what a plan purchase records on the subscription: the plan and its allowance.
type subscriptionPlanTerms struct {
	PlanID  int64
	GroupID int64
	Name    string
	Price   float64
	Limits  SubscriptionLimits
}

// apply records the plan and its allowance on the subscription; an allowance the plan does not set is cleared,
// so that window follows the group.
func (t *subscriptionPlanTerms) apply(sub *UserSubscription) {
	if t == nil || sub == nil {
		return
	}
	planID := t.PlanID
	sub.PlanID = &planID
	sub.DailyLimitUSD = copyPositiveLimit(t.Limits.DailyUSD)
	sub.WeeklyLimitUSD = copyPositiveLimit(t.Limits.WeeklyUSD)
	sub.MonthlyLimitUSD = copyPositiveLimit(t.Limits.MonthlyUSD)
}

func copyPositiveLimit(v *float64) *float64 {
	if !positiveLimit(v) {
		return nil
	}
	out := *v
	return &out
}

type planPurchaseKind int

const (
	// planPurchaseNew: no subscription yet; a new one starts now.
	planPurchaseNew planPurchaseKind = iota
	// planPurchaseRenew: the same plan (or a subscription not bought as a plan, or one that has ended): the
	// existing renewal rules apply — an active term is extended, an ended one starts again now.
	planPurchaseRenew
	// planPurchaseUpgrade: another plan at the same or a higher price while the current one runs: the new plan
	// starts now with a fresh term and the old plan's unused allowance is credited to the balance.
	planPurchaseUpgrade
	// planPurchaseDowngrade: a cheaper plan while the current one runs. Not offered during the period (TASK-57):
	// the user can buy it once the current plan has ended.
	planPurchaseDowngrade
)

func (k planPurchaseKind) String() string {
	switch k {
	case planPurchaseNew:
		return "new"
	case planPurchaseRenew:
		return "renew"
	case planPurchaseUpgrade:
		return "upgrade"
	case planPurchaseDowngrade:
		return "downgrade"
	default:
		return "unknown"
	}
}

// classifyPlanPurchase decides what buying newPlanID (at newPrice) does to the user's existing subscription in
// the plan's group. currentPlanPrice is the price of the plan the subscription was bought with, nil when that
// plan no longer exists.
func classifyPlanPurchase(existing *UserSubscription, now time.Time, newPlanID int64, newPrice float64, currentPlanPrice *float64) planPurchaseKind {
	if existing == nil {
		return planPurchaseNew
	}
	if existing.Status == SubscriptionStatusExpired || !existing.ExpiresAt.After(now) {
		return planPurchaseRenew
	}
	if existing.PlanID == nil || *existing.PlanID == newPlanID {
		return planPurchaseRenew
	}
	if currentPlanPrice == nil || newPrice >= *currentPlanPrice {
		return planPurchaseUpgrade
	}
	return planPurchaseDowngrade
}

const subscriptionMonthlyWindow = 30 * 24 * time.Hour

// planUpgradeCredit is what an upgrade credits to the balance for the old plan (TASK-57): the unused part of the
// monthly allowance in the current 30-day window, in proportion to the time left in that window, plus the whole
// allowance of any later window already paid for (an earlier renewal), in proportion to its length. Only the
// monthly allowance counts; a plan without one (unlimited) credits nothing. Rounded down to cents.
func planUpgradeCredit(sub *UserSubscription, group *Group, now time.Time) float64 {
	if sub == nil || !sub.ExpiresAt.After(now) {
		return 0
	}
	limits := sub.EffectiveLimits(group)
	if !limits.HasMonthly() {
		return 0
	}
	allowance := *limits.MonthlyUSD

	previous := sub.MonthlyWindowStart
	used := sub.MonthlyUsageUSD
	if previous == nil {
		// Never used: the windows count from the start of the term.
		startsAt := sub.StartsAt
		previous = &startsAt
		used = 0
	}
	windowStart := sub.windowResetAnchor(*previous)
	if rolled, ok := sub.automaticWindowStartAt(previous, subscriptionMonthlyWindow, now); ok {
		// The window has moved on but the stored usage is still the old window's.
		windowStart = rolled
		used = 0
	}
	windowEnd := windowStart.Add(subscriptionMonthlyWindow)
	if windowEnd.After(sub.ExpiresAt) {
		windowEnd = sub.ExpiresAt
	}

	credit := 0.0
	if length := windowEnd.Sub(windowStart); length > 0 && windowEnd.After(now) {
		left := windowEnd.Sub(now)
		if left > length {
			left = length
		}
		credit += math.Max(0, allowance-used) * float64(left) / float64(length)
	}
	if later := sub.ExpiresAt.Sub(maxTime(windowEnd, now)); later > 0 {
		credit += allowance * float64(later) / float64(subscriptionMonthlyWindow)
	}
	return math.Floor(credit*100+1e-9) / 100
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// ErrPlanDowngradeNotAllowed: a cheaper plan cannot be bought while the current plan runs (TASK-57). The user
// can buy it once the current plan has ended.
var ErrPlanDowngradeNotAllowed = infraerrors.Conflict("PLAN_DOWNGRADE_NOT_ALLOWED",
	"your current plan is higher than this one; you can switch to the cheaper plan once the current plan ends")

func planDowngradeError(current *subscriptionPlanTerms, sub *UserSubscription) error {
	md := map[string]string{"available_at": sub.ExpiresAt.UTC().Format(time.RFC3339)}
	if current != nil && current.Name != "" {
		md["current_plan"] = current.Name
	}
	return ErrPlanDowngradeNotAllowed.WithMetadata(md)
}

// planTermsLookup returns a plan's terms, or nil when the plan no longer exists.
type planTermsLookup func(ctx context.Context, planID int64) (*subscriptionPlanTerms, error)

// planPurchaseOutcome is what buying a plan did to the subscription.
type planPurchaseOutcome struct {
	Kind           planPurchaseKind
	SubscriptionID int64
	PreviousPlan   *subscriptionPlanTerms
	// Credit is the old plan's unused allowance to credit to the balance (upgrades only).
	Credit float64
}

// applyPlanPurchase applies a paid plan to the user's subscription in the plan's group (TASK-57). It must run in
// the caller's transaction (payment fulfillment), which also records the credit and the audit entry, so a retry
// either sees everything or nothing.
//   - no subscription: a new one with the plan's allowance;
//   - the same plan, a subscription not bought as a plan, or one that has ended: the existing renewal rules
//     (extend an active term by the plan's days; restart an ended one now), recording the plan as it is now;
//   - another plan at the same or a higher price while the current one runs: the new plan starts now with a
//     fresh term and usage, and Credit is the old plan's unused allowance (see planUpgradeCredit);
//   - a cheaper plan while the current one runs: ErrPlanDowngradeNotAllowed, nothing changes.
func (s *SubscriptionService) applyPlanPurchase(ctx context.Context, input *AssignSubscriptionInput, terms *subscriptionPlanTerms, lookup planTermsLookup) (*planPurchaseOutcome, error) {
	if input == nil || terms == nil {
		return nil, ErrSubscriptionNilInput
	}
	group, err := s.groupRepo.GetByID(ctx, input.GroupID)
	if err != nil {
		return nil, fmt.Errorf("group not found: %w", err)
	}
	if !group.IsSubscriptionType() {
		return nil, ErrGroupNotSubscriptionType
	}
	validityDays := normalizeAssignValidityDays(input.ValidityDays)

	existing, err := s.userSubRepo.GetByUserIDAndGroupID(ctx, input.UserID, input.GroupID)
	if err != nil && !errors.Is(err, ErrSubscriptionNotFound) {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	if existing == nil {
		create := *input
		create.ValidityDays = validityDays
		create.planTerms = terms
		sub, err := s.createSubscription(ctx, &create)
		if err != nil {
			return nil, err
		}
		return &planPurchaseOutcome{Kind: planPurchaseNew, SubscriptionID: sub.ID}, nil
	}

	locked, err := s.userSubRepo.GetByIDForUpdate(ctx, existing.ID)
	if err != nil {
		return nil, fmt.Errorf("lock subscription for plan purchase: %w", err)
	}
	now := s.now()
	var current *subscriptionPlanTerms
	var currentPrice *float64
	if locked.PlanID != nil && *locked.PlanID != terms.PlanID && lookup != nil {
		if current, err = lookup(ctx, *locked.PlanID); err != nil {
			return nil, fmt.Errorf("load current plan: %w", err)
		}
		if current != nil {
			price := current.Price
			currentPrice = &price
		}
	}

	kind := classifyPlanPurchase(locked, now, terms.PlanID, terms.Price, currentPrice)
	outcome := &planPurchaseOutcome{Kind: kind, SubscriptionID: locked.ID, PreviousPlan: current}
	switch kind {
	case planPurchaseDowngrade:
		return nil, planDowngradeError(current, locked)
	case planPurchaseUpgrade:
		outcome.Credit = planUpgradeCredit(locked, group, now)
		expiresAt := now.AddDate(0, 0, validityDays)
		if expiresAt.After(MaxExpiresAt) {
			expiresAt = MaxExpiresAt
		}
		upgraded := renewedSubscriptionTerm(locked, input.Notes, now, expiresAt)
		terms.apply(upgraded)
		if err := s.userSubRepo.Update(ctx, upgraded); err != nil {
			return nil, fmt.Errorf("upgrade subscription: %w", err)
		}
		return outcome, nil
	default:
		if err := s.updateExistingSubscriptionTerm(ctx, locked.ID, validityDays, input.Notes, false); err != nil {
			return nil, err
		}
		renewed, err := s.userSubRepo.GetByIDForUpdate(ctx, locked.ID)
		if err != nil {
			return nil, fmt.Errorf("reload renewed subscription: %w", err)
		}
		terms.apply(renewed)
		if err := s.userSubRepo.Update(ctx, renewed); err != nil {
			return nil, fmt.Errorf("record plan on subscription: %w", err)
		}
		return outcome, nil
	}
}
