package payment

import "context"

// CardOnFileGateway is what a payment provider offers to keep a customer's card and charge it later without the
// customer being present (Otoha automatic top-up, TASK-75). Only Stripe cards implement it. The card number never
// reaches us: the customer types it into the provider's own form and we keep the provider's ids, the brand and the
// last four digits.
type CardOnFileGateway interface {
	// PublishableKey is the key the browser needs to show the provider's card form.
	PublishableKey() string
	// Currency is the currency this provider charges in.
	Currency() string
	// EnsureCustomer returns the provider's customer for this user, creating it once: calling it again for the same
	// key returns the same customer.
	EnsureCustomer(ctx context.Context, req CardCustomerRequest) (string, error)
	// CreateCardSetup starts saving a card for the customer, without charging it.
	CreateCardSetup(ctx context.Context, customerID string) (*CardSetup, error)
	// FinishCardSetup reads a finished card setup and returns the card it saved. It fails when the setup is not
	// complete, belongs to another customer, or saved something other than a card.
	FinishCardSetup(ctx context.Context, setupID, customerID string) (*SavedCard, error)
	// LookupCard reads a card the customer paid with. It fails when the card belongs to another customer.
	LookupCard(ctx context.Context, paymentMethodID, customerID string) (*SavedCard, error)
	// RemoveCard stops the provider from keeping the card.
	RemoveCard(ctx context.Context, paymentMethodID string) error
	// ChargeSavedCard charges the saved card without the customer present. A refusal is a result, not an error;
	// an error means the provider could not be reached or did not answer, and the outcome is unknown.
	ChargeSavedCard(ctx context.Context, req SavedCardCharge) (*SavedCardChargeResult, error)
}

// CardCustomerRequest identifies the customer to create.
type CardCustomerRequest struct {
	// Key makes creation repeatable: the same key always returns the same customer.
	Key    string
	UserID int64
	Email  string
}

// CardSetup is what the browser needs to finish saving a card.
type CardSetup struct {
	SetupID      string
	ClientSecret string
}

// SavedCard is a card kept by the provider. Only these display fields are ever stored or shown.
type SavedCard struct {
	PaymentMethodID string
	Brand           string
	Last4           string
	ExpMonth        int
	ExpYear         int
}

// SavedCardCharge is one charge of a saved card without the customer present.
type SavedCardCharge struct {
	// OrderID is the order's out_trade_no; the provider returns it in its payment notification.
	OrderID         string
	Amount          string // formatted for the provider's currency
	Currency        string
	CustomerID      string
	PaymentMethodID string
	Description     string
}

// Outcomes of a saved-card charge.
const (
	SavedCardChargeSucceeded      = "succeeded"
	SavedCardChargeProcessing     = "processing"      // not decided yet; a notification follows
	SavedCardChargeActionRequired = "action_required" // the bank wants the customer to confirm
	SavedCardChargeDeclined       = "declined"        // the card or the bank refused
	SavedCardChargeFailed         = "failed"          // anything else that did not take the money
)

// SavedCardChargeResult is the provider's answer to a charge.
type SavedCardChargeResult struct {
	Status  string
	TradeNo string
	// Code is the provider's reason for a refusal, e.g. card_declined or authentication_required.
	Code    string
	Message string
}
