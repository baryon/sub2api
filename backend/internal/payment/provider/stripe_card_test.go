//go:build unit

package provider

import (
	"bytes"
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v85"
)

// stripeCardBackend answers Stripe calls from a script and records what was sent. It stands in for Stripe: no
// network, no real card.
type stripeCardBackend struct {
	calls []stripeCardCall
	reply func(path string, params stripe.ParamsContainer, v stripe.LastResponseSetter) error
}

type stripeCardCall struct {
	method string
	path   string
	params stripe.ParamsContainer
}

func (b *stripeCardBackend) Call(method string, path string, _ string, params stripe.ParamsContainer, v stripe.LastResponseSetter) error {
	b.calls = append(b.calls, stripeCardCall{method: method, path: path, params: params})
	return b.reply(path, params, v)
}

func (*stripeCardBackend) CallStreaming(string, string, string, stripe.ParamsContainer, stripe.StreamingLastResponseSetter) error {
	return nil
}

func (*stripeCardBackend) CallRaw(string, string, string, []byte, *stripe.Params, stripe.LastResponseSetter) error {
	return nil
}

func (*stripeCardBackend) CallMultipart(string, string, string, string, *bytes.Buffer, *stripe.Params, stripe.LastResponseSetter) error {
	return nil
}

func (*stripeCardBackend) SetMaxNetworkRetries(int64) {}

func newStripeCardTestProvider(b *stripeCardBackend) *Stripe {
	return &Stripe{
		config:      map[string]string{"currency": "USD", "publishableKey": "pk_test_1"},
		initialized: true,
		sc:          stripe.NewClient("sk_test", stripe.WithBackends(&stripe.Backends{API: b})),
	}
}

func TestStripeChargeSavedCardIsOffSessionAndKeyedByOrder(t *testing.T) {
	backend := &stripeCardBackend{reply: func(_ string, _ stripe.ParamsContainer, v stripe.LastResponseSetter) error {
		pi := v.(*stripe.PaymentIntent)
		pi.ID = "pi_auto_1"
		pi.Status = stripe.PaymentIntentStatusSucceeded
		return nil
	}}
	p := newStripeCardTestProvider(backend)

	res, err := p.ChargeSavedCard(context.Background(), payment.SavedCardCharge{
		OrderID: "sub2_20261013abc", Amount: "20.00", CustomerID: "cus_1", PaymentMethodID: "pm_1", Description: "Top-up",
	})
	require.NoError(t, err)
	require.Equal(t, payment.SavedCardChargeSucceeded, res.Status)
	require.Equal(t, "pi_auto_1", res.TradeNo)

	require.Len(t, backend.calls, 1)
	params := backend.calls[0].params.(*stripe.PaymentIntentCreateParams)
	require.Equal(t, int64(2000), *params.Amount)
	require.Equal(t, "usd", *params.Currency)
	require.Equal(t, "cus_1", *params.Customer)
	require.Equal(t, "pm_1", *params.PaymentMethod)
	require.True(t, *params.Confirm, "the charge is confirmed at once")
	require.True(t, *params.OffSession, "the customer is not there")
	require.Equal(t, []*string{stripe.String("card")}, params.PaymentMethodTypes)
	require.Equal(t, "sub2_20261013abc", params.Metadata["orderId"], "the payment notification finds the order by this")
	require.Equal(t, "pi-auto-sub2_20261013abc", *params.IdempotencyKey, "one order can never be charged twice")
}

func TestStripeChargeSavedCardReportsRefusalsAsResults(t *testing.T) {
	tests := []struct {
		name       string
		err        *stripe.Error
		wantStatus string
		wantCode   string
		wantTrade  string
	}{
		{
			name: "bank wants the customer to confirm",
			err: &stripe.Error{Type: stripe.ErrorTypeCard, Code: stripe.ErrorCodeAuthenticationRequired, Msg: "authentication required",
				PaymentIntent: &stripe.PaymentIntent{ID: "pi_sca", Status: stripe.PaymentIntentStatusRequiresPaymentMethod}},
			wantStatus: payment.SavedCardChargeActionRequired,
			wantCode:   "authentication_required",
			wantTrade:  "pi_sca",
		},
		{
			name:       "card declined",
			err:        &stripe.Error{Type: stripe.ErrorTypeCard, Code: stripe.ErrorCodeCardDeclined, Msg: "declined"},
			wantStatus: payment.SavedCardChargeDeclined,
			wantCode:   "card_declined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &stripeCardBackend{reply: func(string, stripe.ParamsContainer, stripe.LastResponseSetter) error { return tt.err }}
			res, err := newStripeCardTestProvider(backend).ChargeSavedCard(context.Background(), payment.SavedCardCharge{
				OrderID: "o1", Amount: "5.00", CustomerID: "cus_1", PaymentMethodID: "pm_1",
			})
			require.NoError(t, err, "a refusal is an answer, not a failure to reach Stripe")
			require.Equal(t, tt.wantStatus, res.Status)
			require.Equal(t, tt.wantCode, res.Code)
			require.Equal(t, tt.wantTrade, res.TradeNo)
		})
	}
}

func TestStripeChargeSavedCardMapsIntentStatuses(t *testing.T) {
	tests := map[stripe.PaymentIntentStatus]string{
		stripe.PaymentIntentStatusProcessing:            payment.SavedCardChargeProcessing,
		stripe.PaymentIntentStatusRequiresAction:        payment.SavedCardChargeActionRequired,
		stripe.PaymentIntentStatusRequiresPaymentMethod: payment.SavedCardChargeDeclined,
		stripe.PaymentIntentStatusCanceled:              payment.SavedCardChargeFailed,
	}
	for status, want := range tests {
		backend := &stripeCardBackend{reply: func(_ string, _ stripe.ParamsContainer, v stripe.LastResponseSetter) error {
			pi := v.(*stripe.PaymentIntent)
			pi.ID, pi.Status = "pi_x", status
			return nil
		}}
		res, err := newStripeCardTestProvider(backend).ChargeSavedCard(context.Background(), payment.SavedCardCharge{
			OrderID: "o1", Amount: "5.00", CustomerID: "cus_1", PaymentMethodID: "pm_1",
		})
		require.NoError(t, err)
		require.Equal(t, want, res.Status, string(status))
	}
}

func TestStripeChargeSavedCardTreatsUnreachableStripeAsAnError(t *testing.T) {
	backend := &stripeCardBackend{reply: func(string, stripe.ParamsContainer, stripe.LastResponseSetter) error {
		return &stripe.Error{Type: stripe.ErrorTypeAPI, Msg: "boom"}
	}}
	res, err := newStripeCardTestProvider(backend).ChargeSavedCard(context.Background(), payment.SavedCardCharge{
		OrderID: "o1", Amount: "5.00", CustomerID: "cus_1", PaymentMethodID: "pm_1",
	})
	require.Error(t, err, "the outcome is unknown, so it is not reported as a refusal")
	require.Nil(t, res)
}

func TestStripeCreateCardSetupKeepsOnlyCardsForLaterUse(t *testing.T) {
	backend := &stripeCardBackend{reply: func(_ string, _ stripe.ParamsContainer, v stripe.LastResponseSetter) error {
		si := v.(*stripe.SetupIntent)
		si.ID, si.ClientSecret = "seti_1", "seti_1_secret"
		return nil
	}}
	setup, err := newStripeCardTestProvider(backend).CreateCardSetup(context.Background(), "cus_1")
	require.NoError(t, err)
	require.Equal(t, "seti_1", setup.SetupID)
	require.Equal(t, "seti_1_secret", setup.ClientSecret)

	params := backend.calls[0].params.(*stripe.SetupIntentCreateParams)
	require.Equal(t, "cus_1", *params.Customer)
	require.Equal(t, "off_session", *params.Usage)
	require.Equal(t, []*string{stripe.String("card")}, params.PaymentMethodTypes)
}

func TestStripeFinishCardSetupChecksStatusCustomerAndType(t *testing.T) {
	card := &stripe.PaymentMethod{ID: "pm_1", Type: stripe.PaymentMethodTypeCard,
		Card: &stripe.PaymentMethodCard{Brand: stripe.PaymentMethodCardBrandVisa, Last4: "4242", ExpMonth: 12, ExpYear: 2030}}
	tests := []struct {
		name    string
		si      stripe.SetupIntent
		wantErr string
	}{
		{name: "saved card", si: stripe.SetupIntent{Status: stripe.SetupIntentStatusSucceeded, Customer: &stripe.Customer{ID: "cus_1"}, PaymentMethod: card}},
		{name: "not finished", si: stripe.SetupIntent{Status: stripe.SetupIntentStatusRequiresAction, Customer: &stripe.Customer{ID: "cus_1"}, PaymentMethod: card}, wantErr: "not succeeded"},
		{name: "another customer", si: stripe.SetupIntent{Status: stripe.SetupIntentStatusSucceeded, Customer: &stripe.Customer{ID: "cus_other"}, PaymentMethod: card}, wantErr: "another customer"},
		{name: "not a card", si: stripe.SetupIntent{Status: stripe.SetupIntentStatusSucceeded, Customer: &stripe.Customer{ID: "cus_1"},
			PaymentMethod: &stripe.PaymentMethod{ID: "pm_2", Type: stripe.PaymentMethodTypeSEPADebit}}, wantErr: "other than a card"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &stripeCardBackend{reply: func(_ string, _ stripe.ParamsContainer, v stripe.LastResponseSetter) error {
				*(v.(*stripe.SetupIntent)) = tt.si
				return nil
			}}
			saved, err := newStripeCardTestProvider(backend).FinishCardSetup(context.Background(), "seti_1", "cus_1")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, payment.SavedCard{PaymentMethodID: "pm_1", Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030}, *saved)
		})
	}
}

func TestStripeCreatePaymentCanKeepTheCardForLaterCharges(t *testing.T) {
	backend := &stripeCardBackend{reply: func(_ string, _ stripe.ParamsContainer, v stripe.LastResponseSetter) error {
		pi := v.(*stripe.PaymentIntent)
		pi.ID, pi.ClientSecret = "pi_1", "pi_1_secret"
		return nil
	}}
	p := newStripeCardTestProvider(backend)

	_, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "o1", Amount: "20.00", Subject: "Top-up", InstanceSubMethods: "card,alipay", SaveCardCustomerID: "cus_1",
	})
	require.NoError(t, err)
	params := backend.calls[0].params.(*stripe.PaymentIntentCreateParams)
	require.Equal(t, "cus_1", *params.Customer)
	require.Equal(t, "off_session", *params.SetupFutureUsage)
	require.Equal(t, []*string{stripe.String("card")}, params.PaymentMethodTypes, "only cards can be kept")
	require.Equal(t, "1", params.Metadata["saveCard"])

	backend.calls = nil
	_, err = p.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "o2", Amount: "20.00", Subject: "Top-up", InstanceSubMethods: "card,alipay"})
	require.NoError(t, err)
	params = backend.calls[0].params.(*stripe.PaymentIntentCreateParams)
	require.Nil(t, params.Customer, "a payment that did not ask to keep the card keeps nothing")
	require.Nil(t, params.SetupFutureUsage)
	require.Len(t, params.PaymentMethodTypes, 2)
}

func TestStripeNotificationCarriesTheKeptCardAndTheFailureReason(t *testing.T) {
	event := func(raw string) *stripe.Event {
		return &stripe.Event{Type: "payment_intent.succeeded", Data: &stripe.EventData{Raw: []byte(raw)}}
	}

	kept, err := parseStripePaymentIntent(event(`{"id":"pi_1","amount":2000,"currency":"usd","customer":"cus_1","payment_method":"pm_1","metadata":{"orderId":"o1","saveCard":"1"}}`), payment.ProviderStatusSuccess, "raw")
	require.NoError(t, err)
	require.Equal(t, "o1", kept.OrderID)
	require.Equal(t, 20.0, kept.Amount)
	require.Equal(t, map[string]string{"currency": "USD", "save_card": "1", "customer": "cus_1", "payment_method": "pm_1"}, kept.Metadata)

	plain, err := parseStripePaymentIntent(event(`{"id":"pi_2","amount":2000,"currency":"usd","customer":"cus_1","payment_method":"pm_1","metadata":{"orderId":"o2"}}`), payment.ProviderStatusSuccess, "raw")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"currency": "USD"}, plain.Metadata, "a payment that did not ask to keep its card reports no card")

	failed, err := parseStripePaymentIntent(event(`{"id":"pi_3","amount":2000,"currency":"usd","metadata":{"orderId":"o3"},"last_payment_error":{"code":"card_declined","decline_code":"insufficient_funds","type":"card_error"}}`), payment.ProviderStatusFailed, "raw")
	require.NoError(t, err)
	require.Equal(t, "card_declined", failed.Metadata["failure_code"])
}
