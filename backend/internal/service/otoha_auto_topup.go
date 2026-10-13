package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Automatic top-up for Otoha users (TASK-75, OtohaAI design "credit top-up" section 4). Off by default. The user
// saves one card on Stripe, turns it on and sets: add `amount` whenever the balance falls below `threshold`, at
// most `monthly cap` a month. After each balance deduction that leaves the balance low, the saved card is
// charged without the user present. The money is credited through the same order fulfillment as a top-up the
// user starts, so a payment notification arriving later changes nothing. Any failure, or a bank asking the user
// to confirm, turns automatic top-up off and tells the user.

// Defaults offered to a user who has not changed anything.
const (
	AutoTopUpDefaultThresholdUSD  = 5.0
	AutoTopUpDefaultAmountUSD     = 20.0
	AutoTopUpDefaultMonthlyCapUSD = 100.0

	autoTopUpMaxThresholdUSD  = 1000.0
	autoTopUpMaxMonthlyCapUSD = 10000.0

	// A charge pending for longer than this has lost its result; a newer one may start.
	autoTopUpPendingStale = 15 * time.Minute
	// How long a server process trusts what it read about a user before reading again.
	autoTopUpCacheTTL      = 30 * time.Second
	autoTopUpChargeTimeout = 90 * time.Second
	autoTopUpManagePath    = "/otoha/account#auto-topup"
)

// Charge statuses.
const (
	AutoTopUpChargePending        = "pending"
	AutoTopUpChargeSucceeded      = "succeeded"
	AutoTopUpChargeFailed         = "failed"
	AutoTopUpChargeRequiresAction = "requires_action"
)

// Reasons automatic top-up was turned off; these are what the website and emails turn into plain words.
const (
	AutoTopUpReasonAuthenticationRequired = "authentication_required"
	AutoTopUpReasonCardDeclined           = "card_declined"
	AutoTopUpReasonPaymentError           = "payment_error"
	// AutoTopUpReasonNoResult marks a charge whose result never came back.
	AutoTopUpReasonNoResult = "no_result"
)

var (
	ErrAutoTopUpInvalidSettings = infraerrors.BadRequest("AUTO_TOPUP_INVALID",
		"These amounts cannot be used for automatic top-up")
	ErrAutoTopUpNeedsCard = infraerrors.BadRequest("AUTO_TOPUP_NEEDS_CARD",
		"Save a bank card before turning on automatic top-up")
	ErrAutoTopUpUnavailable = infraerrors.New(http.StatusServiceUnavailable, "AUTO_TOPUP_UNAVAILABLE",
		"Automatic top-up needs bank card payments, which are not available right now")
	ErrAutoTopUpCardNotSaved = infraerrors.BadRequest("AUTO_TOPUP_CARD_NOT_SAVED",
		"The card was not saved. Add the card again, then try once more")
)

// AutoTopUpSettings are what the user chooses.
type AutoTopUpSettings struct {
	Enabled       bool
	ThresholdUSD  float64
	AmountUSD     float64
	MonthlyCapUSD float64
}

// AutoTopUpRecord is one user's stored row.
type AutoTopUpRecord struct {
	UserID        int64
	Enabled       bool
	ThresholdUSD  float64
	AmountUSD     float64
	MonthlyCapUSD float64
	// ProviderInstanceID is the Stripe account holding the customer and card.
	ProviderInstanceID string
	CustomerID         string
	PaymentMethodID    string
	CardBrand          string
	CardLast4          string
	CardExpMonth       int
	CardExpYear        int
	DisabledReason     string
	DisabledAt         *time.Time
}

// AutoTopUpCard is a card to store with the customer it belongs to.
type AutoTopUpCard struct {
	InstanceID      string
	CustomerID      string
	PaymentMethodID string
	Brand           string
	Last4           string
	ExpMonth        int
	ExpYear         int
}

// AutoTopUpCharge is one attempt to charge the saved card.
type AutoTopUpCharge struct {
	ID         int64
	UserID     int64
	OrderID    *int64
	AmountUSD  float64
	Status     string
	Reason     string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

// AutoTopUpClaimRequest asks to start a charge. Now and the month are given so that the repository does not
// read the clock.
type AutoTopUpClaimRequest struct {
	UserID      int64
	Now         time.Time
	StaleBefore time.Time
	MonthStart  time.Time
	MonthEnd    time.Time
}

// AutoTopUpSkip says why no charge was started.
type AutoTopUpSkip string

const (
	AutoTopUpSkipNone       AutoTopUpSkip = ""
	AutoTopUpSkipNotEnabled AutoTopUpSkip = "not_enabled"
	AutoTopUpSkipNoCard     AutoTopUpSkip = "no_card"
	AutoTopUpSkipBalanceOK  AutoTopUpSkip = "balance_ok"
	AutoTopUpSkipInFlight   AutoTopUpSkip = "in_flight"
	AutoTopUpSkipCapReached AutoTopUpSkip = "cap_reached"
)

// AutoTopUpClaim is the result of starting a charge: the settings as they were when it started and, when a
// charge may go ahead, the new pending charge. UsedUSD is what this month has already used.
type AutoTopUpClaim struct {
	Record  *AutoTopUpRecord
	Charge  *AutoTopUpCharge
	UsedUSD float64
}

// AutoTopUpSummary is this month's use and the latest attempt.
type AutoTopUpSummary struct {
	UsedUSD float64
	Last    *AutoTopUpCharge
}

// AutoTopUpRepository stores settings, the saved card and the charge attempts.
type AutoTopUpRepository interface {
	// Get returns nil when the user has never set anything.
	Get(ctx context.Context, userID int64) (*AutoTopUpRecord, error)
	// SaveSettings creates the row if needed. Turning it on clears the reason it was turned off before.
	SaveSettings(ctx context.Context, userID int64, s AutoTopUpSettings) (*AutoTopUpRecord, error)
	// SaveCustomer stores the Stripe customer. A customer of another Stripe account drops the saved card and turns
	// automatic top-up off, since the card belonged to the old account.
	SaveCustomer(ctx context.Context, userID int64, instanceID, customerID string) error
	SaveCard(ctx context.Context, userID int64, card AutoTopUpCard) error
	// ClearCard forgets the card and turns automatic top-up off.
	ClearCard(ctx context.Context, userID int64) error
	// Disable turns automatic top-up off and records why.
	Disable(ctx context.Context, userID int64, reason string, now time.Time) error
	// BeginCharge decides, under a lock on the user's row, whether a charge may start, and records it as pending
	// in the same step: automatic top-up is on and has a card, the real balance is below the threshold, no other
	// charge is pending, and one more charge stays within the monthly cap. Pending charges older than
	// StaleBefore are first marked failed.
	BeginCharge(ctx context.Context, req AutoTopUpClaimRequest) (*AutoTopUpClaim, AutoTopUpSkip, error)
	AttachOrder(ctx context.Context, chargeID, orderID int64) error
	// FinishCharge settles a pending charge; any other charge is left as it is.
	FinishCharge(ctx context.Context, chargeID int64, status, reason string, now time.Time) error
	// SettleByOrder settles the charge of an order from a payment notification. changed is false when there is no
	// such charge (the order was not an automatic top-up) or it was settled already. A charge that failed only
	// because its answer never arrived can still turn out paid.
	SettleByOrder(ctx context.Context, orderID int64, status, reason string, now time.Time) (charge *AutoTopUpCharge, changed bool, err error)
	// Summary counts pending and succeeded charges in [monthStart, monthEnd) and returns the latest attempt.
	Summary(ctx context.Context, userID int64, monthStart, monthEnd time.Time) (*AutoTopUpSummary, error)
}

// AutoTopUpOrder is the order made for one automatic charge.
type AutoTopUpOrder struct {
	ID         int64
	UserID     int64
	OutTradeNo string
	// PayAmount is what Stripe charges, formatted for its currency; PayAmountValue is the same as a number.
	PayAmount      string
	PayAmountValue float64
	Currency       string
	CreditedUSD    float64
}

// autoTopUpPayments is what automatic top-up needs from the payment service.
type autoTopUpPayments interface {
	// CardGateway returns Stripe for the account holding a card. An empty instance picks the account new cards go to.
	CardGateway(ctx context.Context, instanceID string) (payment.CardOnFileGateway, string, error)
	// CardPaymentsAvailable says whether paying by bank card is possible now.
	CardPaymentsAvailable(ctx context.Context) bool
	// AmountLimits are the smallest and largest top-up the site accepts; 0 means no limit.
	AmountLimits(ctx context.Context) (min, max float64)
	CreateAutoTopUpOrder(ctx context.Context, userID int64, instanceID string, amountUSD float64) (*AutoTopUpOrder, error)
	// SettleAutoTopUp credits a paid order through the normal fulfillment.
	SettleAutoTopUp(ctx context.Context, order *AutoTopUpOrder, tradeNo string) error
	// CancelAutoTopUpOrder closes an order whose charge did not go through.
	CancelAutoTopUpOrder(ctx context.Context, order *AutoTopUpOrder, reason string)
}

type autoTopUpMailer interface {
	Send(ctx context.Context, input NotificationEmailSendInput) error
	ResolveRecipientLocale(ctx context.Context, userID int64, email string) string
}

type autoTopUpSettingReader interface {
	GetValue(ctx context.Context, key string) (string, error)
}

type autoTopUpCacheEntry struct {
	record    *AutoTopUpRecord
	fetchedAt time.Time
	// capMonth is the month in which a charge was refused because of the cap, until capUntil.
	capMonth string
	capUntil time.Time
}

// OtohaAutoTopUpService is automatic top-up.
type OtohaAutoTopUpService struct {
	repo     AutoTopUpRepository
	payments autoTopUpPayments
	users    otohaUserReader
	mail     autoTopUpMailer
	settings autoTopUpSettingReader

	now   func() time.Time
	spawn func(func())

	mu      sync.Mutex
	cache   map[int64]autoTopUpCacheEntry
	running map[int64]struct{}
}

// NewOtohaAutoTopUpService creates the service. mail and settings may be nil: no emails are sent and links stay
// relative.
func NewOtohaAutoTopUpService(repo AutoTopUpRepository, payments autoTopUpPayments, users otohaUserReader, mail autoTopUpMailer, settings autoTopUpSettingReader) *OtohaAutoTopUpService {
	return &OtohaAutoTopUpService{
		repo: repo, payments: payments, users: users, mail: mail, settings: settings,
		now:     time.Now,
		spawn:   func(f func()) { go f() },
		cache:   map[int64]autoTopUpCacheEntry{},
		running: map[int64]struct{}{},
	}
}

// --- What the user sees ---

// OtohaAutoTopUp is automatic top-up as the website and the app show it. It never carries a card number or
// Stripe's ids.
type OtohaAutoTopUp struct {
	Enabled          bool                `json:"enabled"`
	ThresholdUSD     float64             `json:"threshold_usd"`
	AmountUSD        float64             `json:"amount_usd"`
	MonthlyCapUSD    float64             `json:"monthly_cap_usd"`
	UsedThisMonthUSD float64             `json:"used_this_month_usd"`
	Card             *OtohaAutoTopUpCard `json:"card"`
	// DisabledReason is why the system turned it off (authentication_required, card_declined, payment_error);
	// empty when it is on, or when the user turned it off.
	DisabledReason string              `json:"disabled_reason"`
	DisabledAt     *time.Time          `json:"disabled_at"`
	LastTopUp      *OtohaAutoTopUpLast `json:"last_top_up"`
	ManageURL      string              `json:"manage_url"`
	// CardPaymentsAvailable is only set where the website needs it.
	CardPaymentsAvailable *bool `json:"card_payments_available,omitempty"`
}

// OtohaAutoTopUpCard is the saved card as shown: brand and the last four digits.
type OtohaAutoTopUpCard struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int    `json:"exp_month"`
	ExpYear  int    `json:"exp_year"`
}

// OtohaAutoTopUpLast is the latest charge attempt.
type OtohaAutoTopUpLast struct {
	At        time.Time `json:"at"`
	AmountUSD float64   `json:"amount_usd"`
	// Result is pending, succeeded, failed or requires_action.
	Result string `json:"result"`
}

// State reads automatic top-up for one user, with the defaults when nothing was ever set.
func (s *OtohaAutoTopUpService) State(ctx context.Context, userID int64) (*OtohaAutoTopUp, error) {
	rec, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load auto top-up: %w", err)
	}
	monthStart, monthEnd := autoTopUpMonth(s.now())
	summary, err := s.repo.Summary(ctx, userID, monthStart, monthEnd)
	if err != nil {
		return nil, fmt.Errorf("load auto top-up summary: %w", err)
	}
	state := &OtohaAutoTopUp{
		ThresholdUSD:     AutoTopUpDefaultThresholdUSD,
		AmountUSD:        AutoTopUpDefaultAmountUSD,
		MonthlyCapUSD:    AutoTopUpDefaultMonthlyCapUSD,
		UsedThisMonthUSD: summary.UsedUSD,
		ManageURL:        s.manageURL(ctx),
	}
	if rec != nil {
		state.Enabled = rec.Enabled
		state.ThresholdUSD, state.AmountUSD, state.MonthlyCapUSD = rec.ThresholdUSD, rec.AmountUSD, rec.MonthlyCapUSD
		state.DisabledReason, state.DisabledAt = rec.DisabledReason, rec.DisabledAt
		if rec.PaymentMethodID != "" {
			state.Card = &OtohaAutoTopUpCard{Brand: rec.CardBrand, Last4: rec.CardLast4, ExpMonth: rec.CardExpMonth, ExpYear: rec.CardExpYear}
		}
	}
	if summary.Last != nil {
		state.LastTopUp = &OtohaAutoTopUpLast{At: summary.Last.CreatedAt, AmountUSD: summary.Last.AmountUSD, Result: summary.Last.Status}
	}
	return state, nil
}

// StateForWebsite is State plus whether card payments can be used now.
func (s *OtohaAutoTopUpService) StateForWebsite(ctx context.Context, userID int64) (*OtohaAutoTopUp, error) {
	state, err := s.State(ctx, userID)
	if err != nil {
		return nil, err
	}
	available := s.payments.CardPaymentsAvailable(ctx)
	state.CardPaymentsAvailable = &available
	return state, nil
}

func (s *OtohaAutoTopUpService) manageURL(ctx context.Context) string {
	if s.settings != nil {
		for _, key := range []string{SettingKeyFrontendURL, SettingKeyAPIBaseURL} {
			if v, err := s.settings.GetValue(ctx, key); err == nil && strings.TrimSpace(v) != "" {
				return strings.TrimRight(strings.TrimSpace(v), "/") + autoTopUpManagePath
			}
		}
	}
	return autoTopUpManagePath
}

// --- Settings ---

// UpdateSettings validates and saves what the user chose.
func (s *OtohaAutoTopUpService) UpdateSettings(ctx context.Context, userID int64, in AutoTopUpSettings) (*OtohaAutoTopUp, error) {
	if err := s.validateSettings(ctx, in); err != nil {
		return nil, err
	}
	if in.Enabled {
		rec, err := s.repo.Get(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load auto top-up: %w", err)
		}
		if rec == nil || rec.PaymentMethodID == "" {
			return nil, ErrAutoTopUpNeedsCard
		}
		if !s.payments.CardPaymentsAvailable(ctx) {
			return nil, ErrAutoTopUpUnavailable
		}
	}
	if _, err := s.repo.SaveSettings(ctx, userID, in); err != nil {
		return nil, fmt.Errorf("save auto top-up: %w", err)
	}
	s.forget(userID)
	return s.StateForWebsite(ctx, userID)
}

func (s *OtohaAutoTopUpService) validateSettings(ctx context.Context, in AutoTopUpSettings) error {
	minAmount, maxAmount := s.payments.AmountLimits(ctx)
	if minAmount < 1 {
		minAmount = 1
	}
	if maxAmount <= 0 || maxAmount > autoTopUpMaxMonthlyCapUSD {
		maxAmount = autoTopUpMaxMonthlyCapUSD
	}
	bad := func(field, rule string) error {
		return ErrAutoTopUpInvalidSettings.WithMetadata(map[string]string{"field": field, "rule": rule})
	}
	for field, v := range map[string]float64{"threshold": in.ThresholdUSD, "amount": in.AmountUSD, "monthly_cap": in.MonthlyCapUSD} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return bad(field, "positive")
		}
		if math.Abs(v*100-math.Round(v*100)) > 1e-6 {
			return bad(field, "cents")
		}
	}
	if in.ThresholdUSD > autoTopUpMaxThresholdUSD {
		return bad("threshold", "max:"+formatUSD(autoTopUpMaxThresholdUSD))
	}
	if in.AmountUSD < minAmount {
		return bad("amount", "min:"+formatUSD(minAmount))
	}
	if in.AmountUSD > maxAmount {
		return bad("amount", "max:"+formatUSD(maxAmount))
	}
	if in.MonthlyCapUSD < in.AmountUSD {
		return bad("monthly_cap", "min:"+formatUSD(in.AmountUSD))
	}
	if in.MonthlyCapUSD > autoTopUpMaxMonthlyCapUSD {
		return bad("monthly_cap", "max:"+formatUSD(autoTopUpMaxMonthlyCapUSD))
	}
	return nil
}

func formatUSD(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// --- Saving a card ---

// CardSetupStart is what the browser needs to show Stripe's card form.
type CardSetupStart struct {
	ClientSecret   string `json:"client_secret"`
	PublishableKey string `json:"publishable_key"`
}

// StartCardSetup begins saving a card from the settings page. Nothing is charged.
func (s *OtohaAutoTopUpService) StartCardSetup(ctx context.Context, userID int64) (*CardSetupStart, error) {
	gw, instanceID, err := s.payments.CardGateway(ctx, "")
	if err != nil {
		slog.Warn("auto top-up: no card gateway", "user_id", userID, "error", err)
		return nil, ErrAutoTopUpUnavailable
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	customerID, err := s.ensureCustomer(ctx, userID, user.Email, instanceID, gw)
	if err != nil {
		return nil, err
	}
	setup, err := gw.CreateCardSetup(ctx, customerID)
	if err != nil {
		slog.Error("auto top-up: card setup failed", "user_id", userID, "error", err)
		return nil, ErrAutoTopUpUnavailable
	}
	return &CardSetupStart{ClientSecret: setup.ClientSecret, PublishableKey: gw.PublishableKey()}, nil
}

// CustomerForCardSave gives the Stripe customer for a top-up the user starts and wants to keep the card of.
func (s *OtohaAutoTopUpService) CustomerForCardSave(ctx context.Context, userID int64, email, instanceID string, gw payment.CardOnFileGateway) (string, error) {
	return s.ensureCustomer(ctx, userID, email, instanceID, gw)
}

// ensureCustomer returns the user's Stripe customer in this Stripe account, creating it once.
func (s *OtohaAutoTopUpService) ensureCustomer(ctx context.Context, userID int64, email, instanceID string, gw payment.CardOnFileGateway) (string, error) {
	rec, err := s.repo.Get(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("load auto top-up: %w", err)
	}
	if rec != nil && rec.ProviderInstanceID == instanceID && rec.CustomerID != "" {
		return rec.CustomerID, nil
	}
	customerID, err := gw.EnsureCustomer(ctx, payment.CardCustomerRequest{
		Key: fmt.Sprintf("u%d-i%s", userID, instanceID), UserID: userID, Email: email,
	})
	if err != nil {
		return "", fmt.Errorf("create card customer: %w", err)
	}
	if err := s.repo.SaveCustomer(ctx, userID, instanceID, customerID); err != nil {
		return "", fmt.Errorf("save card customer: %w", err)
	}
	s.forget(userID)
	return customerID, nil
}

// FinishCardSetup stores the card Stripe saved after the browser confirmed the form.
func (s *OtohaAutoTopUpService) FinishCardSetup(ctx context.Context, userID int64, setupID string) (*OtohaAutoTopUp, error) {
	rec, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load auto top-up: %w", err)
	}
	if rec == nil || rec.CustomerID == "" || strings.TrimSpace(setupID) == "" {
		return nil, ErrAutoTopUpCardNotSaved
	}
	gw, instanceID, err := s.payments.CardGateway(ctx, rec.ProviderInstanceID)
	if err != nil {
		slog.Warn("auto top-up: no card gateway", "user_id", userID, "error", err)
		return nil, ErrAutoTopUpUnavailable
	}
	card, err := gw.FinishCardSetup(ctx, setupID, rec.CustomerID)
	if err != nil {
		slog.Warn("auto top-up: card setup not finished", "user_id", userID, "error", err)
		return nil, ErrAutoTopUpCardNotSaved
	}
	if err := s.storeCard(ctx, userID, instanceID, rec.CustomerID, card, rec.PaymentMethodID, gw); err != nil {
		return nil, err
	}
	return s.StateForWebsite(ctx, userID)
}

func (s *OtohaAutoTopUpService) storeCard(ctx context.Context, userID int64, instanceID, customerID string, card *payment.SavedCard, oldPaymentMethodID string, gw payment.CardOnFileGateway) error {
	err := s.repo.SaveCard(ctx, userID, AutoTopUpCard{
		InstanceID: instanceID, CustomerID: customerID, PaymentMethodID: card.PaymentMethodID,
		Brand: card.Brand, Last4: card.Last4, ExpMonth: card.ExpMonth, ExpYear: card.ExpYear,
	})
	if err != nil {
		return fmt.Errorf("save card: %w", err)
	}
	s.forget(userID)
	if oldPaymentMethodID != "" && oldPaymentMethodID != card.PaymentMethodID {
		if err := gw.RemoveCard(ctx, oldPaymentMethodID); err != nil {
			slog.Warn("auto top-up: old card was not removed from Stripe", "user_id", userID, "error", err)
		}
	}
	return nil
}

// RemoveCard forgets the saved card and turns automatic top-up off.
func (s *OtohaAutoTopUpService) RemoveCard(ctx context.Context, userID int64) (*OtohaAutoTopUp, error) {
	rec, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load auto top-up: %w", err)
	}
	if rec != nil && rec.PaymentMethodID != "" {
		if gw, _, gwErr := s.payments.CardGateway(ctx, rec.ProviderInstanceID); gwErr == nil {
			if err := gw.RemoveCard(ctx, rec.PaymentMethodID); err != nil {
				slog.Warn("auto top-up: card was not removed from Stripe", "user_id", userID, "error", err)
			}
		}
	}
	if err := s.repo.ClearCard(ctx, userID); err != nil {
		return nil, fmt.Errorf("remove card: %w", err)
	}
	s.forget(userID)
	return s.StateForWebsite(ctx, userID)
}

// --- Payment notifications ---

// OnPaymentConfirmed runs after a payment was confirmed and its order credited, whether the notification came
// first or the automatic charge's own answer did. It settles the order's charge, and keeps the card of a
// payment that asked to keep it.
func (s *OtohaAutoTopUpService) OnPaymentConfirmed(ctx context.Context, order *dbent.PaymentOrder, metadata map[string]string) {
	if s == nil || order == nil {
		return
	}
	if _, changed, err := s.repo.SettleByOrder(ctx, order.ID, AutoTopUpChargeSucceeded, "", s.now()); err != nil {
		slog.Error("auto top-up: settle charge failed", "order_id", order.ID, "error", err)
	} else if changed {
		s.forget(order.UserID)
	}
	if metadata[payment.NotificationMetaSaveCard] == "1" && order.OrderType == payment.OrderTypeBalance {
		s.keepPaymentCard(ctx, order, metadata)
	}
}

func (s *OtohaAutoTopUpService) keepPaymentCard(ctx context.Context, order *dbent.PaymentOrder, metadata map[string]string) {
	customerID, paymentMethodID := metadata[payment.NotificationMetaCustomer], metadata[payment.NotificationMetaPaymentMethod]
	if customerID == "" || paymentMethodID == "" {
		return
	}
	rec, err := s.repo.Get(ctx, order.UserID)
	if err != nil || rec == nil || rec.CustomerID != customerID {
		// The customer was made by us for this user; anything else is not ours to keep.
		return
	}
	if rec.PaymentMethodID == paymentMethodID {
		return
	}
	instanceID := rec.ProviderInstanceID
	if order.ProviderInstanceID != nil && strings.TrimSpace(*order.ProviderInstanceID) != "" {
		instanceID = strings.TrimSpace(*order.ProviderInstanceID)
	}
	gw, instanceID, err := s.payments.CardGateway(ctx, instanceID)
	if err != nil {
		slog.Warn("auto top-up: no card gateway to keep the card", "order_id", order.ID, "error", err)
		return
	}
	card, err := gw.LookupCard(ctx, paymentMethodID, customerID)
	if err != nil {
		slog.Warn("auto top-up: card of the payment was not kept", "order_id", order.ID, "error", err)
		return
	}
	if err := s.storeCard(ctx, order.UserID, instanceID, customerID, card, rec.PaymentMethodID, gw); err != nil {
		slog.Error("auto top-up: card of the payment was not stored", "order_id", order.ID, "error", err)
	}
}

// OnPaymentFailed runs when Stripe reports a payment failed after the automatic charge was left waiting.
func (s *OtohaAutoTopUpService) OnPaymentFailed(ctx context.Context, order *dbent.PaymentOrder, code string) {
	if s == nil || order == nil {
		return
	}
	category := autoTopUpReasonFor(code)
	status := AutoTopUpChargeFailed
	if category == AutoTopUpReasonAuthenticationRequired {
		status = AutoTopUpChargeRequiresAction
	}
	charge, changed, err := s.repo.SettleByOrder(ctx, order.ID, status, firstNonEmpty(code, category), s.now())
	if err != nil {
		slog.Error("auto top-up: settle failed charge", "order_id", order.ID, "error", err)
		return
	}
	if charge == nil || !changed {
		return
	}
	rec, err := s.repo.Get(ctx, charge.UserID)
	if err != nil || rec == nil {
		return
	}
	s.turnOff(ctx, rec, charge, category)
}

// --- Charging ---

// OnBalanceDeducted is called after each balance deduction with the balance that is left. When the balance is
// below the user's threshold, it charges the saved card. It returns when the attempt is over; callers run it
// away from the request.
func (s *OtohaAutoTopUpService) OnBalanceDeducted(ctx context.Context, userID int64, newBalance float64) {
	if s == nil || userID <= 0 {
		return
	}
	rec, capped := s.lookup(ctx, userID)
	if rec == nil || !rec.Enabled || rec.PaymentMethodID == "" || newBalance >= rec.ThresholdUSD || capped {
		return
	}
	if !s.startRunning(userID) {
		return
	}
	defer s.stopRunning(userID)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("auto top-up: panic", "user_id", userID, "recover", r)
		}
	}()
	chargeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), autoTopUpChargeTimeout)
	defer cancel()
	s.charge(chargeCtx, userID)
}

func (s *OtohaAutoTopUpService) charge(ctx context.Context, userID int64) {
	now := s.now()
	monthStart, monthEnd := autoTopUpMonth(now)
	claim, skip, err := s.repo.BeginCharge(ctx, AutoTopUpClaimRequest{
		UserID: userID, Now: now, StaleBefore: now.Add(-autoTopUpPendingStale), MonthStart: monthStart, MonthEnd: monthEnd,
	})
	if err != nil {
		slog.Error("auto top-up: could not start a charge", "user_id", userID, "error", err)
		return
	}
	switch skip {
	case AutoTopUpSkipNone:
	case AutoTopUpSkipCapReached:
		s.noteCapReached(ctx, userID, claim, now)
		return
	case AutoTopUpSkipNotEnabled, AutoTopUpSkipNoCard:
		s.forget(userID)
		return
	default:
		return
	}

	rec, charge := claim.Record, claim.Charge
	gw, instanceID, err := s.payments.CardGateway(ctx, rec.ProviderInstanceID)
	if err != nil {
		slog.Error("auto top-up: no card gateway", "user_id", userID, "error", err)
		s.fail(ctx, rec, charge, nil, AutoTopUpChargeFailed, AutoTopUpReasonPaymentError, AutoTopUpReasonPaymentError)
		return
	}
	order, err := s.payments.CreateAutoTopUpOrder(ctx, userID, instanceID, rec.AmountUSD)
	if err != nil {
		slog.Error("auto top-up: could not create the order", "user_id", userID, "error", err)
		s.fail(ctx, rec, charge, nil, AutoTopUpChargeFailed, AutoTopUpReasonPaymentError, AutoTopUpReasonPaymentError)
		return
	}
	if err := s.repo.AttachOrder(ctx, charge.ID, order.ID); err != nil {
		slog.Error("auto top-up: could not link the order", "user_id", userID, "order_id", order.ID, "error", err)
		s.fail(ctx, rec, charge, order, AutoTopUpChargeFailed, AutoTopUpReasonPaymentError, AutoTopUpReasonPaymentError)
		return
	}

	res, err := gw.ChargeSavedCard(ctx, payment.SavedCardCharge{
		OrderID: order.OutTradeNo, Amount: order.PayAmount, Currency: order.Currency,
		CustomerID: rec.CustomerID, PaymentMethodID: rec.PaymentMethodID, Description: "Otoha AI automatic top-up",
	})
	if err != nil {
		// The answer is unknown. Stripe may still have taken the money; if so its notification credits the order.
		slog.Error("auto top-up: no answer from the payment provider", "user_id", userID, "order_id", order.ID, "error", err)
		s.fail(ctx, rec, charge, order, AutoTopUpChargeFailed, AutoTopUpReasonPaymentError, AutoTopUpReasonPaymentError)
		return
	}
	switch res.Status {
	case payment.SavedCardChargeSucceeded:
		if err := s.payments.SettleAutoTopUp(ctx, order, res.TradeNo); err != nil {
			// The money is taken. The charge stays pending, which blocks another one, until the retry of the
			// fulfillment (the payment notification) completes the order.
			slog.Error("auto top-up: paid but not credited yet", "user_id", userID, "order_id", order.ID, "error", err)
			return
		}
		if err := s.repo.FinishCharge(ctx, charge.ID, AutoTopUpChargeSucceeded, "", s.now()); err != nil {
			slog.Error("auto top-up: could not record the charge", "charge_id", charge.ID, "error", err)
		}
		s.forget(userID)
	case payment.SavedCardChargeProcessing:
		// Not decided yet; the payment notification settles it.
	case payment.SavedCardChargeActionRequired:
		s.fail(ctx, rec, charge, order, AutoTopUpChargeRequiresAction, AutoTopUpReasonAuthenticationRequired, firstNonEmpty(res.Code, AutoTopUpReasonAuthenticationRequired))
	case payment.SavedCardChargeDeclined:
		s.fail(ctx, rec, charge, order, AutoTopUpChargeFailed, AutoTopUpReasonCardDeclined, firstNonEmpty(res.Code, AutoTopUpReasonCardDeclined))
	default:
		s.fail(ctx, rec, charge, order, AutoTopUpChargeFailed, AutoTopUpReasonPaymentError, AutoTopUpReasonPaymentError)
	}
}

// fail records a charge that did not go through, closes its order, turns automatic top-up off and tells the user.
func (s *OtohaAutoTopUpService) fail(ctx context.Context, rec *AutoTopUpRecord, charge *AutoTopUpCharge, order *AutoTopUpOrder, status, category, detail string) {
	if err := s.repo.FinishCharge(ctx, charge.ID, status, detail, s.now()); err != nil {
		slog.Error("auto top-up: could not record the failure", "charge_id", charge.ID, "error", err)
	}
	if order != nil {
		s.payments.CancelAutoTopUpOrder(ctx, order, category)
	}
	s.turnOff(ctx, rec, charge, category)
}

func (s *OtohaAutoTopUpService) turnOff(ctx context.Context, rec *AutoTopUpRecord, charge *AutoTopUpCharge, category string) {
	if err := s.repo.Disable(ctx, rec.UserID, category, s.now()); err != nil {
		slog.Error("auto top-up: could not turn off", "user_id", rec.UserID, "error", err)
	}
	s.forget(rec.UserID)
	slog.Warn("auto top-up turned off", "user_id", rec.UserID, "charge_id", charge.ID, "reason", category)
	s.sendStopped(rec, charge, category)
}

// --- Emails ---

func (s *OtohaAutoTopUpService) sendStopped(rec *AutoTopUpRecord, charge *AutoTopUpCharge, category string) {
	if s.mail == nil || s.users == nil {
		return
	}
	s.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		defer cancel()
		user, err := s.users.GetByID(ctx, rec.UserID)
		if err != nil || user == nil {
			slog.Warn("auto top-up: cannot find the user to tell", "user_id", rec.UserID, "error", err)
			return
		}
		locale := s.mail.ResolveRecipientLocale(ctx, user.ID, user.Email)
		err = s.mail.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventAutoTopUpStopped,
			RecipientEmail: user.Email,
			RecipientName:  firstNonEmpty(user.Username, user.Email),
			UserID:         user.ID,
			SourceType:     "otoha_auto_topup",
			SourceID:       strconv.FormatInt(charge.ID, 10),
			Variables: map[string]string{
				"reason_text":   autoTopUpReasonText(locale, category),
				"card_summary":  autoTopUpCardSummary(locale, rec),
				"attempted_usd": formatUSD(charge.AmountUSD),
				"manage_url":    s.manageURL(ctx),
			},
		})
		if err != nil {
			slog.Warn("auto top-up: stop email failed", "user_id", user.ID, "error", err)
		}
	})
}

func (s *OtohaAutoTopUpService) noteCapReached(ctx context.Context, userID int64, claim *AutoTopUpClaim, now time.Time) {
	month := now.UTC().Format("2006-01")
	s.mu.Lock()
	entry := s.cache[userID]
	already := entry.capMonth == month && now.Before(entry.capUntil)
	entry.capMonth, entry.capUntil = month, now.Add(autoTopUpCacheTTL)
	s.cache[userID] = entry
	s.mu.Unlock()
	if already || s.mail == nil || s.users == nil || claim == nil || claim.Record == nil {
		return
	}
	rec, used := claim.Record, claim.UsedUSD
	s.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		defer cancel()
		user, err := s.users.GetByID(ctx, userID)
		if err != nil || user == nil {
			return
		}
		err = s.mail.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventAutoTopUpCapReached,
			RecipientEmail: user.Email,
			RecipientName:  firstNonEmpty(user.Username, user.Email),
			UserID:         user.ID,
			SourceType:     "otoha_auto_topup_cap",
			SourceID:       fmt.Sprintf("%d:%s", userID, month),
			Variables: map[string]string{
				"monthly_cap": formatUSD(rec.MonthlyCapUSD),
				"used_usd":    formatUSD(used),
				"manage_url":  s.manageURL(ctx),
			},
		})
		if err != nil {
			slog.Warn("auto top-up: cap email failed", "user_id", userID, "error", err)
		}
	})
}

func autoTopUpReasonText(locale, category string) string {
	zh := strings.HasPrefix(strings.ToLower(locale), "zh")
	switch category {
	case AutoTopUpReasonAuthenticationRequired:
		if zh {
			return "银行要求本人确认这笔付款，系统无法代为完成。"
		}
		return "Your bank asked you to confirm this payment yourself, which cannot be done automatically."
	case AutoTopUpReasonCardDeclined:
		if zh {
			return "银行卡被拒绝，可能是余额不足、卡已过期或被银行限制。"
		}
		return "Your card was declined. It may be out of funds, expired, or blocked by the bank."
	default:
		if zh {
			return "付款没有完成，这次没有扣款。"
		}
		return "The payment did not go through, and you were not charged."
	}
}

func autoTopUpCardSummary(locale string, rec *AutoTopUpRecord) string {
	if rec == nil || rec.CardLast4 == "" {
		return ""
	}
	brand := autoTopUpBrandName(rec.CardBrand)
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		return fmt.Sprintf("%s，尾号 %s", brand, rec.CardLast4)
	}
	return fmt.Sprintf("%s ending %s", brand, rec.CardLast4)
}

func autoTopUpBrandName(brand string) string {
	switch strings.ToLower(brand) {
	case "visa":
		return "Visa"
	case "mastercard":
		return "Mastercard"
	case "amex":
		return "American Express"
	case "discover":
		return "Discover"
	case "jcb":
		return "JCB"
	case "diners":
		return "Diners Club"
	case "unionpay":
		return "UnionPay"
	case "":
		return "Card"
	default:
		return strings.ToUpper(brand[:1]) + brand[1:]
	}
}

// autoTopUpReasonFor sorts Stripe's reason code into what the user is told.
func autoTopUpReasonFor(code string) string {
	switch code {
	case "authentication_required":
		return AutoTopUpReasonAuthenticationRequired
	case "":
		return AutoTopUpReasonPaymentError
	default:
		return AutoTopUpReasonCardDeclined
	}
}

// --- What this process remembers ---

// lookup returns the user's settings, read at most every autoTopUpCacheTTL, and whether a charge was already
// refused this month because of the cap.
func (s *OtohaAutoTopUpService) lookup(ctx context.Context, userID int64) (rec *AutoTopUpRecord, capped bool) {
	now := s.now()
	s.mu.Lock()
	entry, ok := s.cache[userID]
	s.mu.Unlock()
	if ok && now.Sub(entry.fetchedAt) < autoTopUpCacheTTL && !entry.fetchedAt.IsZero() {
		return entry.record, entry.capMonth == now.UTC().Format("2006-01") && now.Before(entry.capUntil)
	}
	rec, err := s.repo.Get(ctx, userID)
	if err != nil {
		slog.Error("auto top-up: could not read settings", "user_id", userID, "error", err)
		return nil, false
	}
	s.mu.Lock()
	s.cache[userID] = autoTopUpCacheEntry{record: rec, fetchedAt: now}
	s.mu.Unlock()
	return rec, false
}

// forget drops what this process remembers about the user, after a change here.
func (s *OtohaAutoTopUpService) forget(userID int64) {
	s.mu.Lock()
	delete(s.cache, userID)
	s.mu.Unlock()
}

func (s *OtohaAutoTopUpService) startRunning(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.running[userID]; busy {
		return false
	}
	s.running[userID] = struct{}{}
	return true
}

func (s *OtohaAutoTopUpService) stopRunning(userID int64) {
	s.mu.Lock()
	delete(s.running, userID)
	s.mu.Unlock()
}

// autoTopUpMonth is the calendar month (UTC) containing t, as [start, end).
func autoTopUpMonth(t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}
