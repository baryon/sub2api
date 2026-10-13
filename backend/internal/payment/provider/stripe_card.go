package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	stripe "github.com/stripe/stripe-go/v85"
)

// Stripe keeps a customer's card and charges it later without the customer present (Otoha automatic top-up,
// TASK-75). The card number is typed into Stripe's own form; only Stripe's ids, the brand and the last four
// digits come back here.

// stripeMetadataSaveCard marks a payment that keeps its card for later charges.
const stripeMetadataSaveCard = "saveCard"

// PublishableKey is the key the browser needs to show Stripe's card form.
func (s *Stripe) PublishableKey() string { return s.GetPublishableKey() }

// Currency is the currency this Stripe account charges in.
func (s *Stripe) Currency() string { return s.currency() }

// EnsureCustomer creates the Stripe customer once per key: Stripe returns the same customer for a repeated key.
func (s *Stripe) EnsureCustomer(ctx context.Context, req payment.CardCustomerRequest) (string, error) {
	s.ensureInit()
	if strings.TrimSpace(req.Key) == "" {
		return "", errors.New("stripe customer: missing key")
	}
	params := &stripe.CustomerCreateParams{
		Metadata: map[string]string{"userId": fmt.Sprintf("%d", req.UserID)},
	}
	if email := strings.TrimSpace(req.Email); email != "" {
		params.Email = stripe.String(email)
	}
	params.SetIdempotencyKey("cus-" + req.Key)
	params.Context = ctx
	customer, err := s.sc.V1Customers.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("stripe create customer: %w", err)
	}
	return customer.ID, nil
}

// CreateCardSetup starts saving a card for the customer without charging it.
func (s *Stripe) CreateCardSetup(ctx context.Context, customerID string) (*payment.CardSetup, error) {
	s.ensureInit()
	if strings.TrimSpace(customerID) == "" {
		return nil, errors.New("stripe card setup: missing customer")
	}
	params := &stripe.SetupIntentCreateParams{
		Customer:           stripe.String(customerID),
		Usage:              stripe.String("off_session"),
		PaymentMethodTypes: []*string{stripe.String("card")},
	}
	params.Context = ctx
	si, err := s.sc.V1SetupIntents.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("stripe create setup intent: %w", err)
	}
	return &payment.CardSetup{SetupID: si.ID, ClientSecret: si.ClientSecret}, nil
}

// FinishCardSetup reads the finished setup and the card it saved.
func (s *Stripe) FinishCardSetup(ctx context.Context, setupID, customerID string) (*payment.SavedCard, error) {
	s.ensureInit()
	params := &stripe.SetupIntentRetrieveParams{}
	params.AddExpand("payment_method")
	params.Context = ctx
	si, err := s.sc.V1SetupIntents.Retrieve(ctx, setupID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe read setup intent: %w", err)
	}
	if si.Status != stripe.SetupIntentStatusSucceeded {
		return nil, fmt.Errorf("stripe setup intent is %s, not succeeded", si.Status)
	}
	if si.Customer == nil || si.Customer.ID != customerID {
		return nil, errors.New("stripe setup intent belongs to another customer")
	}
	return savedCardFrom(si.PaymentMethod)
}

// LookupCard reads a card the customer paid with.
func (s *Stripe) LookupCard(ctx context.Context, paymentMethodID, customerID string) (*payment.SavedCard, error) {
	s.ensureInit()
	pm, err := s.sc.V1PaymentMethods.Retrieve(ctx, paymentMethodID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe read payment method: %w", err)
	}
	if pm.Customer == nil || pm.Customer.ID != customerID {
		return nil, errors.New("stripe payment method belongs to another customer")
	}
	return savedCardFrom(pm)
}

func savedCardFrom(pm *stripe.PaymentMethod) (*payment.SavedCard, error) {
	if pm == nil || pm.ID == "" {
		return nil, errors.New("stripe saved no payment method")
	}
	if pm.Type != stripe.PaymentMethodTypeCard || pm.Card == nil {
		return nil, errors.New("stripe saved something other than a card")
	}
	return &payment.SavedCard{
		PaymentMethodID: pm.ID,
		Brand:           string(pm.Card.Brand),
		Last4:           pm.Card.Last4,
		ExpMonth:        int(pm.Card.ExpMonth),
		ExpYear:         int(pm.Card.ExpYear),
	}, nil
}

// RemoveCard stops Stripe from keeping the card for the customer.
func (s *Stripe) RemoveCard(ctx context.Context, paymentMethodID string) error {
	s.ensureInit()
	params := &stripe.PaymentMethodDetachParams{}
	params.Context = ctx
	if _, err := s.sc.V1PaymentMethods.Detach(ctx, paymentMethodID, params); err != nil {
		return fmt.Errorf("stripe detach payment method: %w", err)
	}
	return nil
}

// ChargeSavedCard charges the saved card with the customer absent. The key is the order, so a repeated call for
// the same order cannot charge twice.
func (s *Stripe) ChargeSavedCard(ctx context.Context, req payment.SavedCardCharge) (*payment.SavedCardChargeResult, error) {
	s.ensureInit()
	currency := strings.TrimSpace(req.Currency)
	if currency == "" {
		currency = s.currency()
	}
	minor, err := payment.AmountToMinorUnit(req.Amount, currency)
	if err != nil {
		return nil, fmt.Errorf("stripe saved card charge: %w", err)
	}
	params := &stripe.PaymentIntentCreateParams{
		Amount:             stripe.Int64(minor),
		Currency:           stripe.String(strings.ToLower(currency)),
		Customer:           stripe.String(req.CustomerID),
		PaymentMethod:      stripe.String(req.PaymentMethodID),
		PaymentMethodTypes: []*string{stripe.String("card")},
		Confirm:            stripe.Bool(true),
		OffSession:         stripe.Bool(true),
		Description:        stripe.String(req.Description),
		Metadata:           map[string]string{"orderId": req.OrderID},
	}
	params.SetIdempotencyKey("pi-auto-" + req.OrderID)
	params.Context = ctx

	pi, err := s.sc.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		var apiErr *stripe.Error
		if errors.As(err, &apiErr) && apiErr.Type == stripe.ErrorTypeCard {
			return cardErrorResult(apiErr), nil
		}
		return nil, fmt.Errorf("stripe saved card charge: %w", err)
	}
	return chargeResultFromIntent(pi), nil
}

// cardErrorResult turns Stripe's card error into a result: authentication wanted by the bank, or a refusal.
func cardErrorResult(apiErr *stripe.Error) *payment.SavedCardChargeResult {
	res := &payment.SavedCardChargeResult{
		Status:  payment.SavedCardChargeDeclined,
		Code:    string(apiErr.Code),
		Message: apiErr.Msg,
	}
	if apiErr.Code == stripe.ErrorCodeAuthenticationRequired {
		res.Status = payment.SavedCardChargeActionRequired
	}
	if res.Code == "" {
		res.Code = string(apiErr.DeclineCode)
	}
	if apiErr.PaymentIntent != nil {
		res.TradeNo = apiErr.PaymentIntent.ID
		if apiErr.PaymentIntent.Status == stripe.PaymentIntentStatusRequiresAction {
			res.Status = payment.SavedCardChargeActionRequired
		}
	}
	return res
}

func chargeResultFromIntent(pi *stripe.PaymentIntent) *payment.SavedCardChargeResult {
	res := &payment.SavedCardChargeResult{TradeNo: pi.ID}
	switch pi.Status {
	case stripe.PaymentIntentStatusSucceeded:
		res.Status = payment.SavedCardChargeSucceeded
	case stripe.PaymentIntentStatusProcessing:
		res.Status = payment.SavedCardChargeProcessing
	case stripe.PaymentIntentStatusRequiresAction, stripe.PaymentIntentStatusRequiresConfirmation:
		res.Status = payment.SavedCardChargeActionRequired
		res.Code = string(stripe.ErrorCodeAuthenticationRequired)
	case stripe.PaymentIntentStatusRequiresPaymentMethod:
		res.Status = payment.SavedCardChargeDeclined
	default:
		res.Status = payment.SavedCardChargeFailed
	}
	if pi.LastPaymentError != nil {
		res.Code = string(pi.LastPaymentError.Code)
		if res.Code == "" {
			res.Code = string(pi.LastPaymentError.DeclineCode)
		}
		res.Message = pi.LastPaymentError.Msg
	}
	return res
}

var _ payment.CardOnFileGateway = (*Stripe)(nil)
