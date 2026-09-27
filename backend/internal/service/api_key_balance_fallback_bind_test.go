//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A user without a plan, such as a new user with only the starting balance, can bind a key to a
// subscription group that falls back to the balance; other subscription groups still need a plan.
type fallbackBindGroupRepo struct {
	GroupRepository
	groups []Group
}

func (r *fallbackBindGroupRepo) ListActive(context.Context) ([]Group, error) { return r.groups, nil }

func (r *fallbackBindGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	for i := range r.groups {
		if r.groups[i].ID == id {
			return &r.groups[i], nil
		}
	}
	return nil, ErrGroupNotFound
}

type noPlanSubRepo struct {
	UserSubscriptionRepository
}

func (noPlanSubRepo) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return nil, nil
}

func (noPlanSubRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	return nil, ErrSubscriptionNotFound
}

func TestAUserWithoutAPlanCanBindABalanceFallbackGroup(t *testing.T) {
	svc := &APIKeyService{
		userRepo:    &visibilityUserRepo{user: &User{ID: 1}},
		userSubRepo: noPlanSubRepo{},
		groupRepo: &fallbackBindGroupRepo{groups: []Group{
			{ID: 42, Name: "otoha", SubscriptionType: SubscriptionTypeSubscription, BalanceFallbackEnabled: true, Status: StatusActive},
			{ID: 43, Name: "plan only", SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive},
			{ID: 44, Name: "exclusive otoha", SubscriptionType: SubscriptionTypeSubscription, BalanceFallbackEnabled: true, IsExclusive: true, Status: StatusActive},
		}},
	}

	available, err := svc.GetAvailableGroups(context.Background(), 1)
	require.NoError(t, err)
	ids := make([]int64, 0, len(available))
	for _, group := range available {
		ids = append(ids, group.ID)
	}
	require.Equal(t, []int64{42}, ids, "an exclusive group still needs the user to be allowed in")

	group, err := svc.GetBindableGroupForUser(context.Background(), 1, 42)
	require.NoError(t, err)
	require.Equal(t, int64(42), group.ID)
	_, err = svc.GetBindableGroupForUser(context.Background(), 1, 43)
	require.ErrorIs(t, err, ErrGroupNotAllowed)
}
