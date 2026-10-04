package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type otohaRepository struct {
	client *dbent.Client
	db     *sql.DB
}

// NewOtohaRepository stores the Otoha key and claim codes (TASK-55).
func NewOtohaRepository(client *dbent.Client, db *sql.DB) service.OtohaRepository {
	return &otohaRepository{client: client, db: db}
}

// EnsureNamedAPIKey serialises callers per user with a transaction-scoped advisory lock, so a webhook and a
// result-page poll fulfilling at the same moment still leave one key. The key is the system's, issued for a
// payment: it is created here rather than through APIKeyService.Create, so the per-user key count and creation
// rate limits do not apply; it has no IP rules, and the service clears its auth cache entry.
func (r *otohaRepository) EnsureNamedAPIKey(ctx context.Context, userID, groupID int64, name, candidateKey string) (*service.APIKey, bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin otoha key tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	client := tx.Client()
	if client.Driver().Dialect() == dialect.Postgres {
		var rows entsql.Rows
		lockKey := advisoryLockHash("otoha-named-api-key:" + strconv.FormatInt(userID, 10))
		if err := client.Driver().Query(ctx, "SELECT pg_advisory_xact_lock($1)", []any{lockKey}, &rows); err != nil {
			return nil, false, fmt.Errorf("lock otoha key: %w", err)
		}
		_ = rows.Close()
	}

	existing, err := client.APIKey.Query().
		Where(
			apikey.UserIDEQ(userID),
			apikey.GroupIDEQ(groupID),
			apikey.NameEQ(name),
			apikey.DeletedAtIsNil(),
		).
		Order(dbent.Asc(apikey.FieldID)).
		First(ctx)
	switch {
	case err == nil:
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit otoha key lookup: %w", err)
		}
		return apiKeyEntityToService(existing), false, nil
	case !dbent.IsNotFound(err):
		return nil, false, fmt.Errorf("find otoha key: %w", err)
	}

	created, err := client.APIKey.Create().
		SetUserID(userID).
		SetGroupID(groupID).
		SetName(name).
		SetKey(candidateKey).
		SetStatus(service.StatusActive).
		Save(ctx)
	if err != nil {
		return nil, false, translatePersistenceError(err, nil, service.ErrAPIKeyExists)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit otoha key: %w", err)
	}
	return apiKeyEntityToService(created), true, nil
}

func (r *otohaRepository) CreateClaim(ctx context.Context, claim *service.OtohaClaim) error {
	if claim == nil {
		return errors.New("nil otoha claim")
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO otoha_claims (code_hash, user_id, api_key_id, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		claim.CodeHash, claim.UserID, claim.APIKeyID, claim.ExpiresAt,
	).Scan(&claim.ID, &claim.CreatedAt)
}

// ConsumeClaim marks the claim used in the same statement that checks it, so a code works exactly once.
func (r *otohaRepository) ConsumeClaim(ctx context.Context, codeHash string, now time.Time) (*service.OtohaClaim, error) {
	claim := &service.OtohaClaim{}
	var usedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		UPDATE otoha_claims
		SET used_at = $2
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING id, code_hash, user_id, api_key_id, expires_at, used_at, created_at`,
		codeHash, now,
	).Scan(&claim.ID, &claim.CodeHash, &claim.UserID, &claim.APIKeyID, &claim.ExpiresAt, &usedAt, &claim.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrOtohaClaimNotUsable
	}
	if err != nil {
		return nil, err
	}
	if usedAt.Valid {
		t := usedAt.Time
		claim.UsedAt = &t
	}
	return claim, nil
}

func (r *otohaRepository) DeleteExpiredClaims(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM otoha_claims WHERE expires_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *otohaRepository) LatestPlanOrder(ctx context.Context, userID, groupID int64) (*service.OtohaPlanOrder, error) {
	var (
		reference string
		planName  sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT o.out_trade_no, p.name
		FROM payment_orders o
		LEFT JOIN subscription_plans p ON p.id = o.plan_id
		WHERE o.user_id = $1
		  AND o.order_type = $2
		  AND o.subscription_group_id = $3
		  AND o.status = $4
		ORDER BY o.completed_at DESC NULLS LAST, o.id DESC
		LIMIT 1`,
		userID, payment.OrderTypeSubscription, groupID, payment.OrderStatusCompleted,
	).Scan(&reference, &planName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &service.OtohaPlanOrder{PlanName: planName.String, Reference: reference}, nil
}
