//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type otohaRepoFixture struct {
	repo  service.OtohaRepository
	user  *service.User
	group *service.Group
	other *service.Group
}

func newOtohaRepoFixture(t *testing.T) *otohaRepoFixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("otoha-repo-group-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
		SubscriptionType: service.SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
	})
	other := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("otoha-repo-other-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	user := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("otoha-repo-%d@example.com", suffix)})
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM otoha_claims WHERE user_id = $1",
			"DELETE FROM auth_cache_invalidation_outbox WHERE cache_key IN (SELECT encode(sha256(convert_to(key, 'UTF8')), 'hex') FROM api_keys WHERE user_id = $1)",
			"DELETE FROM api_keys WHERE user_id = $1",
			"DELETE FROM payment_orders WHERE user_id = $1",
			"DELETE FROM users WHERE id = $1",
		} {
			_, err := integrationDB.ExecContext(ctx, q, user.ID)
			require.NoError(t, err, q)
		}
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM subscription_plans WHERE group_id = $1", group.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", group.ID, other.ID)
		require.NoError(t, err)
	})
	return &otohaRepoFixture{repo: NewOtohaRepository(integrationEntClient, integrationDB), user: user, group: group, other: other}
}

func TestOtohaRepoEnsureNamedAPIKeyKeepsOneKeyUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	f := newOtohaRepoFixture(t)

	const workers = 8
	results := make([]*service.APIKey, workers)
	created := make([]bool, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], created[i], errs[i] = f.repo.EnsureNamedAPIKey(ctx, f.user.ID, f.group.ID, service.OtohaDesktopKeyName,
				fmt.Sprintf("sk-otoha-repo-%d-%d", f.user.ID, i))
		}(i)
	}
	close(start)
	wg.Wait()

	createdCount := 0
	for i := range workers {
		require.NoError(t, errs[i])
		require.Equal(t, results[0].ID, results[i].ID)
		require.Equal(t, results[0].Key, results[i].Key)
		if created[i] {
			createdCount++
		}
	}
	require.Equal(t, 1, createdCount)

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND deleted_at IS NULL", f.user.ID).Scan(&count))
	require.Equal(t, 1, count)

	key := results[0]
	require.Equal(t, service.OtohaDesktopKeyName, key.Name)
	require.Equal(t, service.StatusActive, key.Status)
	require.NotNil(t, key.GroupID)
	require.Equal(t, f.group.ID, *key.GroupID)
}

func TestOtohaRepoEnsureNamedAPIKeyIgnoresOtherGroupsAndDeletedKeys(t *testing.T) {
	ctx := context.Background()
	f := newOtohaRepoFixture(t)
	otherID := f.other.ID
	mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: f.user.ID, GroupID: &otherID,
		Key: fmt.Sprintf("sk-otoha-other-%d", f.user.ID), Name: service.OtohaDesktopKeyName, Status: service.StatusActive})

	first, created, err := f.repo.EnsureNamedAPIKey(ctx, f.user.ID, f.group.ID, service.OtohaDesktopKeyName, fmt.Sprintf("sk-otoha-a-%d", f.user.ID))
	require.NoError(t, err)
	require.True(t, created, "a key with the same name in another group does not count")

	_, err = integrationDB.ExecContext(ctx, "UPDATE api_keys SET deleted_at = NOW() WHERE id = $1", first.ID)
	require.NoError(t, err)
	second, created, err := f.repo.EnsureNamedAPIKey(ctx, f.user.ID, f.group.ID, service.OtohaDesktopKeyName, fmt.Sprintf("sk-otoha-b-%d", f.user.ID))
	require.NoError(t, err)
	require.True(t, created, "a deleted key is replaced")
	require.NotEqual(t, first.ID, second.ID)
}

func TestOtohaRepoClaimIsUsableOnceAndOnlyBeforeItExpires(t *testing.T) {
	ctx := context.Background()
	f := newOtohaRepoFixture(t)
	key, _, err := f.repo.EnsureNamedAPIKey(ctx, f.user.ID, f.group.ID, service.OtohaDesktopKeyName, fmt.Sprintf("sk-otoha-claim-%d", f.user.ID))
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	hash := fmt.Sprintf("%064d", f.user.ID)
	require.NoError(t, f.repo.CreateClaim(ctx, &service.OtohaClaim{CodeHash: hash, UserID: f.user.ID, APIKeyID: key.ID, ExpiresAt: now.Add(15 * time.Minute)}))

	expiredHash := fmt.Sprintf("e%063d", f.user.ID)
	require.NoError(t, f.repo.CreateClaim(ctx, &service.OtohaClaim{CodeHash: expiredHash, UserID: f.user.ID, APIKeyID: key.ID, ExpiresAt: now.Add(-time.Second)}))

	_, err = f.repo.ConsumeClaim(ctx, expiredHash, now)
	require.ErrorIs(t, err, service.ErrOtohaClaimNotUsable)
	_, err = f.repo.ConsumeClaim(ctx, "unknown", now)
	require.ErrorIs(t, err, service.ErrOtohaClaimNotUsable)

	// Two apps racing with the same code: exactly one gets it.
	const racers = 6
	var wg sync.WaitGroup
	wins := make(chan *service.OtohaClaim, racers)
	losses := make(chan error, racers)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := f.repo.ConsumeClaim(ctx, hash, now)
			if err == nil {
				wins <- claim
				return
			}
			losses <- err
		}()
	}
	wg.Wait()
	close(wins)
	close(losses)
	require.Len(t, wins, 1)
	for err := range losses {
		require.ErrorIs(t, err, service.ErrOtohaClaimNotUsable)
	}
	won := <-wins
	require.Equal(t, f.user.ID, won.UserID)
	require.Equal(t, key.ID, won.APIKeyID)
	require.NotNil(t, won.UsedAt)

	var stored int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM otoha_claims WHERE code_hash = $1 AND used_at IS NOT NULL", hash).Scan(&stored))
	require.Equal(t, 1, stored)

	deleted, err := f.repo.DeleteExpiredClaims(ctx, now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, deleted, int64(1))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM otoha_claims WHERE code_hash = $1", expiredHash).Scan(&stored))
	require.Zero(t, stored)
}

func TestOtohaRepoLatestPlanOrderNamesTheLastCompletedPlan(t *testing.T) {
	ctx := context.Background()
	f := newOtohaRepoFixture(t)

	none, err := f.repo.LatestPlanOrder(ctx, f.user.ID, f.group.ID)
	require.NoError(t, err)
	require.Nil(t, none)

	plus, err := integrationEntClient.SubscriptionPlan.Create().SetGroupID(f.group.ID).SetName("Otoha Plus").SetPrice(20).SetValidityDays(30).Save(ctx)
	require.NoError(t, err)
	pro, err := integrationEntClient.SubscriptionPlan.Create().SetGroupID(f.group.ID).SetName("Otoha Pro").SetPrice(50).SetValidityDays(30).Save(ctx)
	require.NoError(t, err)

	base := time.Now().Add(-48 * time.Hour)
	mustCreateOtohaOrder(t, f, plus.ID, f.group.ID, payment.OrderStatusCompleted, base, "otoha-plus")
	mustCreateOtohaOrder(t, f, pro.ID, f.group.ID, payment.OrderStatusCompleted, base.Add(time.Hour), "otoha-pro")
	mustCreateOtohaOrder(t, f, plus.ID, f.group.ID, payment.OrderStatusPending, base.Add(2*time.Hour), "otoha-pending")
	mustCreateOtohaOrder(t, f, plus.ID, f.other.ID, payment.OrderStatusCompleted, base.Add(3*time.Hour), "otoha-other-group")

	order, err := f.repo.LatestPlanOrder(ctx, f.user.ID, f.group.ID)
	require.NoError(t, err)
	require.NotNil(t, order)
	require.Equal(t, "Otoha Pro", order.PlanName)
	require.Equal(t, fmt.Sprintf("otoha-pro-%d", f.user.ID), order.Reference)
}

func mustCreateOtohaOrder(t *testing.T, f *otohaRepoFixture, planID, groupID int64, status string, completedAt time.Time, ref string) *dbent.PaymentOrder {
	t.Helper()
	create := integrationEntClient.PaymentOrder.Create().
		SetUserID(f.user.ID).SetUserEmail(f.user.Email).SetUserName("otoha").
		SetAmount(20).SetPayAmount(20).SetFeeRate(0).
		SetRechargeCode(fmt.Sprintf("%s-code-%d", ref, f.user.ID)).
		SetOutTradeNo(fmt.Sprintf("%s-%d", ref, f.user.ID)).
		SetPaymentType(payment.TypeStripe).SetPaymentTradeNo("trade-" + ref).
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(planID).SetSubscriptionGroupID(groupID).SetSubscriptionDays(30).
		SetStatus(status).
		SetExpiresAt(completedAt.Add(time.Hour)).
		SetClientIP("127.0.0.1").SetSrcHost("account.example.com")
	if status == payment.OrderStatusCompleted {
		create = create.SetCompletedAt(completedAt)
	}
	order, err := create.Save(context.Background())
	require.NoError(t, err)
	return order
}
