package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Usage is recorded on a background context after the request ends. A composite group's request was routed to one
// provider, and its channel price is looked up under that provider (TASK-62): the provider must survive the hand-off,
// or billing takes another provider's price for the same model name.
func TestUsageRecordContextKeepsTheCompositeProvider(t *testing.T) {
	parent := service.WithCompositeRouteDecision(context.Background(), service.CompositeRouteDecision{
		Matched: true, TargetPlatform: service.PlatformOpenAI, PublicModel: "otoha-fast", UpstreamModel: "gpt-5.4-mini",
	})

	ctx := usageRecordContext(parent, context.Background())
	platform, ok := service.ResolvedTargetPlatformFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, service.PlatformOpenAI, platform)

	_, ok = service.ResolvedTargetPlatformFromContext(usageRecordContext(context.Background(), context.Background()))
	require.False(t, ok, "a request that was not routed carries no provider")
}
