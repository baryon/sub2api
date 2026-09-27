//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Balance fallback applies to subscription groups only: a standard group already charges the
// balance, so the setting is dropped there.
func TestAdminService_CreateGroup_KeepsBalanceFallbackOnlyForSubscriptionGroups(t *testing.T) {
	for _, tt := range []struct {
		name             string
		subscriptionType string
		want             bool
	}{
		{name: "subscription", subscriptionType: SubscriptionTypeSubscription, want: true},
		{name: "standard", subscriptionType: SubscriptionTypeStandard, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			svc := &adminServiceImpl{groupRepo: repo}

			_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name: "otoha-" + tt.name, Platform: PlatformOpenAI, RateMultiplier: 1,
				SubscriptionType: tt.subscriptionType, BalanceFallbackEnabled: true,
			})

			require.NoError(t, err)
			require.Equal(t, tt.want, repo.created.BalanceFallbackEnabled)
		})
	}
}

func TestAdminService_UpdateGroup_TurnsBalanceFallbackOnAndRefreshesCachedKeys(t *testing.T) {
	existing := &Group{ID: 3, Name: "otoha", Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription}
	repo := &groupRepoStubForAdmin{getByID: existing}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: invalidator}
	enabled := true

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{BalanceFallbackEnabled: &enabled})

	require.NoError(t, err)
	require.True(t, repo.updated.BalanceFallbackEnabled)
	require.Equal(t, []int64{existing.ID}, invalidator.groupIDs)
}

func TestAdminService_UpdateGroup_DropsBalanceFallbackWhenTheGroupStopsBeingASubscription(t *testing.T) {
	existing := &Group{ID: 4, Name: "otoha", Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription, BalanceFallbackEnabled: true}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{SubscriptionType: SubscriptionTypeStandard})

	require.NoError(t, err)
	require.False(t, repo.updated.BalanceFallbackEnabled)
}
