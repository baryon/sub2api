//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// fakeAutoTopUpRepo keeps the rows in memory and applies the same rules as the database repository: one lock
// around the claim, a pending charge blocks the next one, and the monthly cap is counted inside the claim.
type fakeAutoTopUpRepo struct {
	mu       sync.Mutex
	records  map[int64]*AutoTopUpRecord
	charges  []*AutoTopUpCharge
	balances map[int64]float64
	nextID   int64
}

func newFakeAutoTopUpRepo() *fakeAutoTopUpRepo {
	return &fakeAutoTopUpRepo{records: map[int64]*AutoTopUpRecord{}, balances: map[int64]float64{}}
}

func (r *fakeAutoTopUpRepo) record(userID int64) *AutoTopUpRecord {
	rec, ok := r.records[userID]
	if !ok {
		rec = &AutoTopUpRecord{UserID: userID, ThresholdUSD: 5, AmountUSD: 20, MonthlyCapUSD: 100}
		r.records[userID] = rec
	}
	return rec
}

func (r *fakeAutoTopUpRepo) Get(_ context.Context, userID int64) (*AutoTopUpRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[userID]
	if !ok {
		return nil, nil
	}
	cp := *rec
	return &cp, nil
}

func (r *fakeAutoTopUpRepo) SaveSettings(_ context.Context, userID int64, s AutoTopUpSettings) (*AutoTopUpRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.record(userID)
	rec.Enabled, rec.ThresholdUSD, rec.AmountUSD, rec.MonthlyCapUSD = s.Enabled, s.ThresholdUSD, s.AmountUSD, s.MonthlyCapUSD
	if s.Enabled {
		rec.DisabledReason, rec.DisabledAt = "", nil
	}
	cp := *rec
	return &cp, nil
}

func (r *fakeAutoTopUpRepo) SaveCustomer(_ context.Context, userID int64, instanceID, customerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.record(userID)
	if rec.ProviderInstanceID != instanceID {
		rec.PaymentMethodID, rec.CardBrand, rec.CardLast4, rec.CardExpMonth, rec.CardExpYear = "", "", "", 0, 0
		rec.Enabled = false
	}
	rec.ProviderInstanceID, rec.CustomerID = instanceID, customerID
	return nil
}

func (r *fakeAutoTopUpRepo) SaveCard(_ context.Context, userID int64, c AutoTopUpCard) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.record(userID)
	rec.ProviderInstanceID, rec.CustomerID = c.InstanceID, c.CustomerID
	rec.PaymentMethodID, rec.CardBrand, rec.CardLast4, rec.CardExpMonth, rec.CardExpYear = c.PaymentMethodID, c.Brand, c.Last4, c.ExpMonth, c.ExpYear
	return nil
}

func (r *fakeAutoTopUpRepo) ClearCard(_ context.Context, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.record(userID)
	rec.PaymentMethodID, rec.CardBrand, rec.CardLast4, rec.CardExpMonth, rec.CardExpYear = "", "", "", 0, 0
	rec.Enabled, rec.DisabledReason, rec.DisabledAt = false, "", nil
	return nil
}

func (r *fakeAutoTopUpRepo) Disable(_ context.Context, userID int64, reason string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.record(userID)
	rec.Enabled, rec.DisabledReason, rec.DisabledAt = false, reason, &now
	return nil
}

func (r *fakeAutoTopUpRepo) BeginCharge(_ context.Context, req AutoTopUpClaimRequest) (*AutoTopUpClaim, AutoTopUpSkip, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[req.UserID]
	if !ok || !rec.Enabled {
		return nil, AutoTopUpSkipNotEnabled, nil
	}
	cp := *rec
	if rec.PaymentMethodID == "" {
		return &AutoTopUpClaim{Record: &cp}, AutoTopUpSkipNoCard, nil
	}
	if r.balances[req.UserID] >= rec.ThresholdUSD {
		return &AutoTopUpClaim{Record: &cp}, AutoTopUpSkipBalanceOK, nil
	}
	used := 0.0
	for _, c := range r.charges {
		if c.UserID != req.UserID {
			continue
		}
		if c.Status == AutoTopUpChargePending && c.CreatedAt.Before(req.StaleBefore) {
			c.Status, c.Reason = AutoTopUpChargeFailed, AutoTopUpReasonNoResult
		}
	}
	for _, c := range r.charges {
		if c.UserID != req.UserID {
			continue
		}
		if c.Status == AutoTopUpChargePending {
			return &AutoTopUpClaim{Record: &cp}, AutoTopUpSkipInFlight, nil
		}
		if c.Status == AutoTopUpChargeSucceeded && !c.CreatedAt.Before(req.MonthStart) && c.CreatedAt.Before(req.MonthEnd) {
			used += c.AmountUSD
		}
	}
	if used+rec.AmountUSD > rec.MonthlyCapUSD+1e-9 {
		return &AutoTopUpClaim{Record: &cp, UsedUSD: used}, AutoTopUpSkipCapReached, nil
	}
	r.nextID++
	charge := &AutoTopUpCharge{ID: r.nextID, UserID: req.UserID, AmountUSD: rec.AmountUSD, Status: AutoTopUpChargePending, CreatedAt: req.Now}
	r.charges = append(r.charges, charge)
	out := *charge
	return &AutoTopUpClaim{Record: &cp, Charge: &out, UsedUSD: used}, AutoTopUpSkipNone, nil
}

func (r *fakeAutoTopUpRepo) AttachOrder(_ context.Context, chargeID, orderID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.charges {
		if c.ID == chargeID {
			id := orderID
			c.OrderID = &id
			return nil
		}
	}
	return errors.New("no such charge")
}

func (r *fakeAutoTopUpRepo) FinishCharge(_ context.Context, chargeID int64, status, reason string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.charges {
		if c.ID == chargeID && c.Status == AutoTopUpChargePending {
			c.Status, c.Reason, c.FinishedAt = status, reason, &now
		}
	}
	return nil
}

func (r *fakeAutoTopUpRepo) SettleByOrder(_ context.Context, orderID int64, status, reason string, now time.Time) (*AutoTopUpCharge, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.charges {
		if c.OrderID == nil || *c.OrderID != orderID {
			continue
		}
		canFlip := c.Status == AutoTopUpChargePending ||
			(status == AutoTopUpChargeSucceeded && c.Status == AutoTopUpChargeFailed &&
				(c.Reason == AutoTopUpReasonPaymentError || c.Reason == AutoTopUpReasonNoResult))
		if !canFlip {
			cp := *c
			return &cp, false, nil
		}
		c.Status, c.Reason, c.FinishedAt = status, reason, &now
		cp := *c
		return &cp, true, nil
	}
	return nil, false, nil
}

func (r *fakeAutoTopUpRepo) Summary(_ context.Context, userID int64, monthStart, monthEnd time.Time) (*AutoTopUpSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sum := &AutoTopUpSummary{}
	for _, c := range r.charges {
		if c.UserID != userID {
			continue
		}
		if (c.Status == AutoTopUpChargeSucceeded || c.Status == AutoTopUpChargePending) && !c.CreatedAt.Before(monthStart) && c.CreatedAt.Before(monthEnd) {
			sum.UsedUSD += c.AmountUSD
		}
		if sum.Last == nil || c.CreatedAt.After(sum.Last.CreatedAt) || (c.CreatedAt.Equal(sum.Last.CreatedAt) && c.ID > sum.Last.ID) {
			cp := *c
			sum.Last = &cp
		}
	}
	return sum, nil
}

// seedCharge adds a finished charge, for tests about the cap and the last top-up.
func (r *fakeAutoTopUpRepo) seedCharge(userID int64, amount float64, status string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.charges = append(r.charges, &AutoTopUpCharge{ID: r.nextID, UserID: userID, AmountUSD: amount, Status: status, CreatedAt: at})
}

func (r *fakeAutoTopUpRepo) chargesFor(userID int64) []AutoTopUpCharge {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []AutoTopUpCharge
	for _, c := range r.charges {
		if c.UserID == userID {
			out = append(out, *c)
		}
	}
	return out
}

func (r *fakeAutoTopUpRepo) setBalance(userID int64, balance float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.balances[userID] = balance
}

// fakeCardGateway stands in for Stripe. It records every call and answers from a script.
type fakeCardGateway struct {
	mu        sync.Mutex
	charges   []payment.SavedCardCharge
	removed   []string
	customers []payment.CardCustomerRequest
	setups    []string

	chargeResult *payment.SavedCardChargeResult
	chargeErr    error
	// hold, when set, makes a charge wait: started is signalled first, then the charge waits for release.
	started chan struct{}
	release chan struct{}

	finishCard *payment.SavedCard
	finishErr  error
	lookupCard *payment.SavedCard
	lookupErr  error
}

func (g *fakeCardGateway) PublishableKey() string { return "pk_test_fake" }
func (g *fakeCardGateway) Currency() string       { return "USD" }

func (g *fakeCardGateway) EnsureCustomer(_ context.Context, req payment.CardCustomerRequest) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.customers = append(g.customers, req)
	return "cus_" + req.Key, nil
}

func (g *fakeCardGateway) CreateCardSetup(_ context.Context, customerID string) (*payment.CardSetup, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.setups = append(g.setups, customerID)
	return &payment.CardSetup{SetupID: "seti_1", ClientSecret: "seti_1_secret"}, nil
}

func (g *fakeCardGateway) FinishCardSetup(_ context.Context, _, _ string) (*payment.SavedCard, error) {
	return g.finishCard, g.finishErr
}

func (g *fakeCardGateway) LookupCard(_ context.Context, _, _ string) (*payment.SavedCard, error) {
	return g.lookupCard, g.lookupErr
}

func (g *fakeCardGateway) RemoveCard(_ context.Context, paymentMethodID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removed = append(g.removed, paymentMethodID)
	return nil
}

func (g *fakeCardGateway) ChargeSavedCard(_ context.Context, req payment.SavedCardCharge) (*payment.SavedCardChargeResult, error) {
	g.mu.Lock()
	g.charges = append(g.charges, req)
	started, release := g.started, g.release
	res, err := g.chargeResult, g.chargeErr
	g.mu.Unlock()
	if started != nil {
		started <- struct{}{}
		<-release
	}
	return res, err
}

func (g *fakeCardGateway) chargeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.charges)
}

// fakeAutoTopUpPayments plays the part of the payment service: it makes orders and credits them.
type fakeAutoTopUpPayments struct {
	mu         sync.Mutex
	gateway    *fakeCardGateway
	available  bool
	min, max   float64
	nextOrder  int64
	orders     []*AutoTopUpOrder
	settled    []string
	cancelled  []string
	settleErr  error
	gatewayErr error
	// onSettle runs when an order is credited, e.g. to raise the user's balance.
	onSettle func(order *AutoTopUpOrder)
}

func (p *fakeAutoTopUpPayments) CardGateway(_ context.Context, instanceID string) (payment.CardOnFileGateway, string, error) {
	if p.gatewayErr != nil {
		return nil, "", p.gatewayErr
	}
	if instanceID == "" {
		instanceID = "7"
	}
	return p.gateway, instanceID, nil
}

func (p *fakeAutoTopUpPayments) CardPaymentsAvailable(context.Context) bool { return p.available }

func (p *fakeAutoTopUpPayments) AmountLimits(context.Context) (float64, float64) { return p.min, p.max }

func (p *fakeAutoTopUpPayments) CreateAutoTopUpOrder(_ context.Context, userID int64, instanceID string, amountUSD float64) (*AutoTopUpOrder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextOrder++
	order := &AutoTopUpOrder{
		ID: p.nextOrder, UserID: userID, OutTradeNo: fmt.Sprintf("sub2_test%d", p.nextOrder),
		PayAmount: fmt.Sprintf("%.2f", amountUSD), PayAmountValue: amountUSD, Currency: "USD", CreditedUSD: amountUSD,
	}
	p.orders = append(p.orders, order)
	return order, nil
}

func (p *fakeAutoTopUpPayments) SettleAutoTopUp(_ context.Context, order *AutoTopUpOrder, tradeNo string) error {
	p.mu.Lock()
	err := p.settleErr
	if err == nil {
		p.settled = append(p.settled, order.OutTradeNo+"/"+tradeNo)
	}
	hook := p.onSettle
	p.mu.Unlock()
	if err == nil && hook != nil {
		hook(order)
	}
	return err
}

func (p *fakeAutoTopUpPayments) CancelAutoTopUpOrder(_ context.Context, order *AutoTopUpOrder, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelled = append(p.cancelled, order.OutTradeNo+"/"+reason)
}

// fakeAutoTopUpMail records the emails a user would get.
type fakeAutoTopUpMail struct {
	mu   sync.Mutex
	sent []NotificationEmailSendInput
}

func (m *fakeAutoTopUpMail) Send(_ context.Context, in NotificationEmailSendInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, in)
	return nil
}

func (m *fakeAutoTopUpMail) ResolveRecipientLocale(context.Context, int64, string) string {
	return "en"
}

func (m *fakeAutoTopUpMail) events() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.sent))
	for _, s := range m.sent {
		out = append(out, s.Event)
	}
	return out
}

type fakeAutoTopUpSettings struct{ values map[string]string }

func (s fakeAutoTopUpSettings) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", errors.New("not set")
}
