package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A group's balance fallback survives the auth cache, or a cached key would refuse requests its
// balance should pay for.
func TestAPIKeyAuthSnapshotGroupBalanceFallbackRoundtrip(t *testing.T) {
	groupID := int64(77)
	apiKey := &APIKey{
		ID: 83, UserID: 41, GroupID: &groupID, Key: "sk-fallback-roundtrip", Status: StatusActive,
		User: &User{ID: 41, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "otoha", Platform: PlatformOpenAI, Status: StatusActive,
			Hydrated: true, SubscriptionType: SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
		},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.True(t, materialized.Group.BalanceFallbackEnabled)
}
