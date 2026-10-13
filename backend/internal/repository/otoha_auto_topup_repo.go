package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type otohaAutoTopUpRepository struct {
	db *sql.DB
}

// NewOtohaAutoTopUpRepository stores automatic top-up settings, the saved card and the charge attempts (TASK-75).
func NewOtohaAutoTopUpRepository(db *sql.DB) service.AutoTopUpRepository {
	return &otohaAutoTopUpRepository{db: db}
}

const autoTopUpColumns = `user_id, enabled, threshold_usd, amount_usd, monthly_cap_usd, provider_instance_id,
	stripe_customer_id, stripe_payment_method_id, card_brand, card_last4, card_exp_month, card_exp_year,
	disabled_reason, disabled_at`

func scanAutoTopUp(row rowScanner) (*service.AutoTopUpRecord, error) {
	rec := &service.AutoTopUpRecord{}
	var disabledAt sql.NullTime
	if err := row.Scan(&rec.UserID, &rec.Enabled, &rec.ThresholdUSD, &rec.AmountUSD, &rec.MonthlyCapUSD, &rec.ProviderInstanceID,
		&rec.CustomerID, &rec.PaymentMethodID, &rec.CardBrand, &rec.CardLast4, &rec.CardExpMonth, &rec.CardExpYear,
		&rec.DisabledReason, &disabledAt); err != nil {
		return nil, err
	}
	if disabledAt.Valid {
		t := disabledAt.Time
		rec.DisabledAt = &t
	}
	return rec, nil
}

func (r *otohaAutoTopUpRepository) Get(ctx context.Context, userID int64) (*service.AutoTopUpRecord, error) {
	rec, err := scanAutoTopUp(r.db.QueryRowContext(ctx, `SELECT `+autoTopUpColumns+` FROM otoha_auto_topups WHERE user_id = $1`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

// SaveSettings writes the user's choice. Turning automatic top-up on clears the reason it was turned off before;
// saving amounts while it stays off keeps that reason.
func (r *otohaAutoTopUpRepository) SaveSettings(ctx context.Context, userID int64, s service.AutoTopUpSettings) (*service.AutoTopUpRecord, error) {
	return scanAutoTopUp(r.db.QueryRowContext(ctx, `
		INSERT INTO otoha_auto_topups (user_id, enabled, threshold_usd, amount_usd, monthly_cap_usd)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			threshold_usd = EXCLUDED.threshold_usd,
			amount_usd = EXCLUDED.amount_usd,
			monthly_cap_usd = EXCLUDED.monthly_cap_usd,
			disabled_reason = CASE WHEN EXCLUDED.enabled THEN '' ELSE otoha_auto_topups.disabled_reason END,
			disabled_at = CASE WHEN EXCLUDED.enabled THEN NULL ELSE otoha_auto_topups.disabled_at END,
			updated_at = NOW()
		RETURNING `+autoTopUpColumns,
		userID, s.Enabled, s.ThresholdUSD, s.AmountUSD, s.MonthlyCapUSD))
}

func (r *otohaAutoTopUpRepository) SaveCustomer(ctx context.Context, userID int64, instanceID, customerID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otoha_auto_topups (user_id, provider_instance_id, stripe_customer_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			enabled = CASE WHEN otoha_auto_topups.provider_instance_id = EXCLUDED.provider_instance_id THEN otoha_auto_topups.enabled ELSE FALSE END,
			stripe_payment_method_id = CASE WHEN otoha_auto_topups.provider_instance_id = EXCLUDED.provider_instance_id THEN otoha_auto_topups.stripe_payment_method_id ELSE '' END,
			card_brand = CASE WHEN otoha_auto_topups.provider_instance_id = EXCLUDED.provider_instance_id THEN otoha_auto_topups.card_brand ELSE '' END,
			card_last4 = CASE WHEN otoha_auto_topups.provider_instance_id = EXCLUDED.provider_instance_id THEN otoha_auto_topups.card_last4 ELSE '' END,
			card_exp_month = CASE WHEN otoha_auto_topups.provider_instance_id = EXCLUDED.provider_instance_id THEN otoha_auto_topups.card_exp_month ELSE 0 END,
			card_exp_year = CASE WHEN otoha_auto_topups.provider_instance_id = EXCLUDED.provider_instance_id THEN otoha_auto_topups.card_exp_year ELSE 0 END,
			provider_instance_id = EXCLUDED.provider_instance_id,
			stripe_customer_id = EXCLUDED.stripe_customer_id,
			updated_at = NOW()`,
		userID, instanceID, customerID)
	return err
}

func (r *otohaAutoTopUpRepository) SaveCard(ctx context.Context, userID int64, c service.AutoTopUpCard) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otoha_auto_topups (user_id, provider_instance_id, stripe_customer_id, stripe_payment_method_id,
			card_brand, card_last4, card_exp_month, card_exp_year)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET
			provider_instance_id = EXCLUDED.provider_instance_id,
			stripe_customer_id = EXCLUDED.stripe_customer_id,
			stripe_payment_method_id = EXCLUDED.stripe_payment_method_id,
			card_brand = EXCLUDED.card_brand,
			card_last4 = EXCLUDED.card_last4,
			card_exp_month = EXCLUDED.card_exp_month,
			card_exp_year = EXCLUDED.card_exp_year,
			updated_at = NOW()`,
		userID, c.InstanceID, c.CustomerID, c.PaymentMethodID, c.Brand, c.Last4, c.ExpMonth, c.ExpYear)
	return err
}

func (r *otohaAutoTopUpRepository) ClearCard(ctx context.Context, userID int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE otoha_auto_topups SET enabled = FALSE, stripe_payment_method_id = '', card_brand = '', card_last4 = '',
			card_exp_month = 0, card_exp_year = 0, disabled_reason = '', disabled_at = NULL, updated_at = NOW()
		WHERE user_id = $1`, userID)
	return err
}

func (r *otohaAutoTopUpRepository) Disable(ctx context.Context, userID int64, reason string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE otoha_auto_topups SET enabled = FALSE, disabled_reason = $2, disabled_at = $3, updated_at = NOW()
		WHERE user_id = $1`, userID, reason, now)
	return err
}

// BeginCharge decides and records in one transaction that holds a lock on the user's row, so two requests that
// both see a low balance are handled one after the other: the second finds the first one's pending charge. The
// partial unique index on pending charges backs this up if a lock were ever skipped.
func (r *otohaAutoTopUpRepository) BeginCharge(ctx context.Context, req service.AutoTopUpClaimRequest) (*service.AutoTopUpClaim, service.AutoTopUpSkip, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, service.AutoTopUpSkipNone, fmt.Errorf("begin auto top-up charge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rec, err := scanAutoTopUp(tx.QueryRowContext(ctx, `SELECT `+autoTopUpColumns+` FROM otoha_auto_topups WHERE user_id = $1 FOR UPDATE`, req.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.AutoTopUpSkipNotEnabled, nil
	}
	if err != nil {
		return nil, service.AutoTopUpSkipNone, err
	}
	if !rec.Enabled {
		return nil, service.AutoTopUpSkipNotEnabled, nil
	}
	if rec.PaymentMethodID == "" {
		return &service.AutoTopUpClaim{Record: rec}, service.AutoTopUpSkipNoCard, nil
	}

	var balance float64
	if err := tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = $1`, req.UserID).Scan(&balance); err != nil {
		return nil, service.AutoTopUpSkipNone, fmt.Errorf("read balance: %w", err)
	}
	if balance >= rec.ThresholdUSD {
		return &service.AutoTopUpClaim{Record: rec}, service.AutoTopUpSkipBalanceOK, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE otoha_auto_topup_charges SET status = 'failed', reason = $3, finished_at = $2
		WHERE user_id = $1 AND status = 'pending' AND created_at < $4`,
		req.UserID, req.Now, service.AutoTopUpReasonNoResult, req.StaleBefore); err != nil {
		return nil, service.AutoTopUpSkipNone, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM otoha_auto_topup_charges WHERE user_id = $1 AND status = 'pending'`, req.UserID).Scan(&pending); err != nil {
		return nil, service.AutoTopUpSkipNone, err
	}
	if pending > 0 {
		return &service.AutoTopUpClaim{Record: rec}, service.AutoTopUpSkipInFlight, nil
	}

	var used float64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_usd), 0) FROM otoha_auto_topup_charges
		WHERE user_id = $1 AND status = 'succeeded' AND created_at >= $2 AND created_at < $3`,
		req.UserID, req.MonthStart, req.MonthEnd).Scan(&used); err != nil {
		return nil, service.AutoTopUpSkipNone, err
	}
	if used+rec.AmountUSD > rec.MonthlyCapUSD+1e-9 {
		return &service.AutoTopUpClaim{Record: rec, UsedUSD: used}, service.AutoTopUpSkipCapReached, nil
	}

	charge := &service.AutoTopUpCharge{UserID: req.UserID, AmountUSD: rec.AmountUSD, Status: service.AutoTopUpChargePending}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO otoha_auto_topup_charges (user_id, amount_usd, status, created_at)
		VALUES ($1, $2, 'pending', $3) RETURNING id, created_at`,
		req.UserID, rec.AmountUSD, req.Now).Scan(&charge.ID, &charge.CreatedAt); err != nil {
		return nil, service.AutoTopUpSkipNone, fmt.Errorf("record charge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, service.AutoTopUpSkipNone, fmt.Errorf("commit auto top-up charge: %w", err)
	}
	return &service.AutoTopUpClaim{Record: rec, Charge: charge, UsedUSD: used}, service.AutoTopUpSkipNone, nil
}

func (r *otohaAutoTopUpRepository) AttachOrder(ctx context.Context, chargeID, orderID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE otoha_auto_topup_charges SET order_id = $2 WHERE id = $1`, chargeID, orderID)
	return err
}

func (r *otohaAutoTopUpRepository) FinishCharge(ctx context.Context, chargeID int64, status, reason string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE otoha_auto_topup_charges SET status = $2, reason = $3, finished_at = $4
		WHERE id = $1 AND status = 'pending'`, chargeID, status, reason, now)
	return err
}

// SettleByOrder settles the charge of an order in one statement. A charge may leave pending once; the only other
// move is a failure whose answer never arrived (payment_error, no_result) turning out paid.
func (r *otohaAutoTopUpRepository) SettleByOrder(ctx context.Context, orderID int64, status, reason string, now time.Time) (*service.AutoTopUpCharge, bool, error) {
	charge, err := scanCharge(r.db.QueryRowContext(ctx, `
		UPDATE otoha_auto_topup_charges SET status = $2, reason = $3, finished_at = $4
		WHERE order_id = $1 AND (status = 'pending'
			OR ($2 = 'succeeded' AND status = 'failed' AND reason IN ('payment_error', 'no_result')))
		RETURNING `+chargeColumns, orderID, status, reason, now))
	if err == nil {
		return charge, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	existing, err := scanCharge(r.db.QueryRowContext(ctx, `SELECT `+chargeColumns+` FROM otoha_auto_topup_charges WHERE order_id = $1`, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

const chargeColumns = `id, user_id, order_id, amount_usd, status, reason, created_at, finished_at`

func scanCharge(row rowScanner) (*service.AutoTopUpCharge, error) {
	c := &service.AutoTopUpCharge{}
	var orderID sql.NullInt64
	var finished sql.NullTime
	if err := row.Scan(&c.ID, &c.UserID, &orderID, &c.AmountUSD, &c.Status, &c.Reason, &c.CreatedAt, &finished); err != nil {
		return nil, err
	}
	if orderID.Valid {
		id := orderID.Int64
		c.OrderID = &id
	}
	if finished.Valid {
		t := finished.Time
		c.FinishedAt = &t
	}
	return c, nil
}

func (r *otohaAutoTopUpRepository) Summary(ctx context.Context, userID int64, monthStart, monthEnd time.Time) (*service.AutoTopUpSummary, error) {
	sum := &service.AutoTopUpSummary{}
	if err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_usd), 0) FROM otoha_auto_topup_charges
		WHERE user_id = $1 AND status IN ('pending', 'succeeded') AND created_at >= $2 AND created_at < $3`,
		userID, monthStart, monthEnd).Scan(&sum.UsedUSD); err != nil {
		return nil, err
	}
	last, err := scanCharge(r.db.QueryRowContext(ctx, `
		SELECT `+chargeColumns+` FROM otoha_auto_topup_charges WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, userID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		sum.Last = last
	}
	return sum, nil
}
