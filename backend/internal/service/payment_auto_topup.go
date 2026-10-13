package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// The payment service's part of Otoha automatic top-up (TASK-75): it makes the order for each automatic charge and
// credits it through the same fulfillment as any other top-up, and tells automatic top-up about the payments it
// confirms or sees fail.

const autoTopUpOperator = "system:auto_topup"

// paymentAutoTopUpHook is automatic top-up as the payment service sees it.
type paymentAutoTopUpHook interface {
	OnPaymentConfirmed(ctx context.Context, order *dbent.PaymentOrder, metadata map[string]string)
	OnPaymentFailed(ctx context.Context, order *dbent.PaymentOrder, code string)
	CustomerForCardSave(ctx context.Context, userID int64, email, instanceID string, gw payment.CardOnFileGateway) (string, error)
}

// SetAutoTopUp attaches automatic top-up (TASK-75).
func (s *PaymentService) SetAutoTopUp(hook paymentAutoTopUpHook) {
	s.autoTopUp = hook
}

var errAutoTopUpNoCardGateway = errors.New("bank card payments through Stripe are not available")

// CardGateway returns Stripe for the account holding a saved card. An empty instance picks the Stripe account
// that new payments go to. A disabled or removed account is an error: its saved cards cannot be charged.
func (s *PaymentService) CardGateway(ctx context.Context, instanceID string) (payment.CardOnFileGateway, string, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		cfg, err := s.configService.GetPaymentConfig(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("get payment config: %w", err)
		}
		sel, err := s.loadBalancer.SelectInstance(ctx, "", payment.TypeStripe, payment.Strategy(cfg.LoadBalanceStrategy), 0)
		if err != nil || sel == nil || sel.ProviderKey != payment.TypeStripe {
			return nil, "", errAutoTopUpNoCardGateway
		}
		return s.cardGatewayFromSelection(sel)
	}
	sel, err := s.stripeSelection(ctx, instanceID)
	if err != nil {
		return nil, "", err
	}
	return s.cardGatewayFromSelection(sel)
}

func (s *PaymentService) cardGatewayFromSelection(sel *payment.InstanceSelection) (payment.CardOnFileGateway, string, error) {
	prov, err := provider.CreateProvider(sel.ProviderKey, sel.InstanceID, sel.Config)
	if err != nil {
		return nil, "", fmt.Errorf("create stripe provider: %w", err)
	}
	gw, ok := prov.(payment.CardOnFileGateway)
	if !ok {
		return nil, "", errAutoTopUpNoCardGateway
	}
	return gw, sel.InstanceID, nil
}

// stripeSelection describes one enabled Stripe instance as an order is created against it.
func (s *PaymentService) stripeSelection(ctx context.Context, instanceID string) (*payment.InstanceSelection, error) {
	id, err := strconv.ParseInt(instanceID, 10, 64)
	if err != nil {
		return nil, errAutoTopUpNoCardGateway
	}
	inst, err := s.entClient.PaymentProviderInstance.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load payment provider instance %d: %w", id, err)
	}
	if !inst.Enabled || inst.ProviderKey != payment.TypeStripe {
		return nil, errAutoTopUpNoCardGateway
	}
	cfg, err := s.loadBalancer.GetInstanceConfig(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load provider instance config: %w", err)
	}
	if inst.PaymentMode != "" {
		cfg["paymentMode"] = inst.PaymentMode
	}
	return &payment.InstanceSelection{
		InstanceID:     instanceID,
		ProviderKey:    inst.ProviderKey,
		Config:         cfg,
		SupportedTypes: inst.SupportedTypes,
		PaymentMode:    inst.PaymentMode,
	}, nil
}

// CardPaymentsAvailable says whether a bank card can be charged through Stripe now.
func (s *PaymentService) CardPaymentsAvailable(ctx context.Context) bool {
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.BalanceDisabled {
		return false
	}
	_, _, err = s.CardGateway(ctx, "")
	return err == nil
}

// AmountLimits are the smallest and largest top-up the site accepts; 0 means no limit.
func (s *PaymentService) AmountLimits(ctx context.Context) (float64, float64) {
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return 0, 0
	}
	return cfg.MinAmount, cfg.MaxAmount
}

// CreateAutoTopUpOrder makes the order for one automatic charge: a balance top-up on a Stripe account, priced
// like one the user starts, with the same limits, fee and bonus.
func (s *PaymentService) CreateAutoTopUpOrder(ctx context.Context, userID int64, instanceID string, amountUSD float64) (*AutoTopUpOrder, error) {
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	req := CreateOrderRequest{
		UserID: userID, Amount: amountUSD, PaymentType: payment.TypeStripe, OrderType: payment.OrderTypeBalance,
		ClientIP: "auto-topup", SrcHost: "auto-topup", PaymentSource: "auto_topup",
	}
	if _, err := s.validateOrderInput(ctx, req, cfg); err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user.Status != payment.EntityStatusActive {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	sel, err := s.stripeSelection(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	currency := paymentProviderConfigCurrency(sel.ProviderKey, sel.Config)
	quote := quoteRechargeBonus(cfg, amountUSD, currency)
	payAmountStr, payAmount, err := calculateCreateOrderPayAmountForOrderType(quote.PayBase, cfg.RechargeFeeRate, currency, payment.OrderTypeBalance, cfg.SubscriptionUSDToCNYRate)
	if err != nil {
		return nil, err
	}
	if err := validateSelectedCreateOrderAmountCurrency(payAmountStr, sel); err != nil {
		return nil, err
	}
	order, err := s.createOrderInTx(ctx, req, user, nil, cfg, quote.Credited, quote.PayBase, cfg.RechargeFeeRate, payAmount, quote.Bonus, sel)
	if err != nil {
		return nil, err
	}
	s.writeAuditLog(ctx, order.ID, "ORDER_CREATED", autoTopUpOperator, map[string]any{
		"creditedAmount": order.Amount, "bonusAmount": order.BonusAmount, "payAmount": order.PayAmount,
		"paymentType": order.PaymentType, "orderType": order.OrderType, "paymentSource": "auto_topup",
	})
	return &AutoTopUpOrder{
		ID: order.ID, UserID: userID, OutTradeNo: order.OutTradeNo,
		PayAmount: payAmountStr, PayAmountValue: payAmount, Currency: currency, CreditedUSD: order.Amount,
	}, nil
}

// SettleAutoTopUp credits a paid order. It goes through the same path as Stripe's own notification, so whichever
// comes second finds the order done.
func (s *PaymentService) SettleAutoTopUp(ctx context.Context, order *AutoTopUpOrder, tradeNo string) error {
	return s.HandlePaymentNotification(ctx, &payment.PaymentNotification{
		TradeNo:  tradeNo,
		OrderID:  order.OutTradeNo,
		Amount:   order.PayAmountValue,
		Status:   payment.NotificationStatusSuccess,
		Metadata: map[string]string{"currency": order.Currency},
	}, payment.TypeStripe)
}

// CancelAutoTopUpOrder closes an order whose charge did not go through. If Stripe's late notification shows the
// money was taken after all, the order is recovered and credited like any late payment.
func (s *PaymentService) CancelAutoTopUpOrder(ctx context.Context, order *AutoTopUpOrder, reason string) {
	n, err := s.entClient.PaymentOrder.Update().
		Where(paymentorder.IDEQ(order.ID), paymentorder.StatusEQ(OrderStatusPending)).
		SetStatus(OrderStatusCancelled).
		SetFailedAt(time.Now()).
		SetFailedReason("auto top-up: " + reason).
		Save(ctx)
	if err != nil {
		slog.Error("auto top-up: could not close the order", "order_id", order.ID, "error", err)
		return
	}
	if n > 0 {
		s.writeAuditLog(ctx, order.ID, "AUTO_TOPUP_FAILED", autoTopUpOperator, map[string]any{"reason": reason})
	}
}

// afterPaymentConfirmed tells automatic top-up that an order was confirmed and fulfilled.
func (s *PaymentService) afterPaymentConfirmed(ctx context.Context, orderID int64, metadata map[string]string) {
	if s.autoTopUp == nil {
		return
	}
	order, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return
	}
	s.autoTopUp.OnPaymentConfirmed(ctx, order, metadata)
}

// afterPaymentFailed tells automatic top-up that Stripe reported a payment failed.
func (s *PaymentService) afterPaymentFailed(ctx context.Context, n *payment.PaymentNotification) {
	if s.autoTopUp == nil || n.Status != payment.ProviderStatusFailed || n.OrderID == "" {
		return
	}
	order, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNo(n.OrderID)).Only(ctx)
	if err != nil {
		return
	}
	s.autoTopUp.OnPaymentFailed(ctx, order, n.Metadata[payment.NotificationMetaFailureCode])
}

// rememberCardForPayment asks Stripe to keep the card of a payment the user started and wants kept.
func (s *PaymentService) rememberCardForPayment(ctx context.Context, req CreateOrderRequest, order *dbent.PaymentOrder, sel *payment.InstanceSelection, prov payment.Provider) string {
	if !req.SaveCard || s.autoTopUp == nil || req.OrderType != payment.OrderTypeBalance || sel == nil || sel.ProviderKey != payment.TypeStripe {
		return ""
	}
	gw, ok := prov.(payment.CardOnFileGateway)
	if !ok {
		return ""
	}
	customerID, err := s.autoTopUp.CustomerForCardSave(ctx, req.UserID, order.UserEmail, sel.InstanceID, gw)
	if err != nil {
		// The payment itself must not fail because the card could not be kept.
		slog.Warn("auto top-up: card will not be kept for this payment", "order_id", order.ID, "error", err)
		return ""
	}
	return customerID
}
