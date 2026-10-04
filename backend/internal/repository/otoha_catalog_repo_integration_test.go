//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The catalog round-trips every field through the migrated table, keeps one entry per model in a group, and goes
// with its group.
func TestOtohaCatalogRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("otoha-catalog-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	other := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("otoha-catalog-other-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM otoha_catalog_models WHERE group_id IN ($1, $2)", group.ID, other.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", group.ID, other.ID)
	})
	repo := NewOtohaCatalogRepository(integrationEntClient)

	full := &service.OtohaCatalogEntry{
		GroupID: group.ID, ModelID: "gpt-6-luna", Name: "GPT-6 Luna", Description: "Everyday work", Enabled: true,
		SortOrder: 20, Inputs: []string{"text", "image"}, Tools: true, Context: 1_050_000, MaxOutput: 128_000,
		Reasoning: []string{"low", "medium"}, DefaultReasoning: "medium", Speed: "fast",
		Strengths: map[string]string{"coding": "strong", "writing": "usable"}, Complexity: "complex",
		Roles: []string{"lead", "execute"}, Use: []string{"default"}, ProfileSource: "vendor", CostTier: "standard",
	}
	require.NoError(t, repo.Create(ctx, full))
	require.NotZero(t, full.ID)
	require.False(t, full.CreatedAt.IsZero())

	plain := &service.OtohaCatalogEntry{GroupID: group.ID, ModelID: "deepseek-v4", Name: "deepseek-v4", SortOrder: 10}
	require.NoError(t, repo.Create(ctx, plain))
	require.NoError(t, repo.Create(ctx, &service.OtohaCatalogEntry{GroupID: other.ID, ModelID: "gpt-6-luna", Name: "x"}))

	dup := &service.OtohaCatalogEntry{GroupID: group.ID, ModelID: "gpt-6-luna", Name: "again"}
	require.ErrorIs(t, repo.Create(ctx, dup), service.ErrOtohaCatalogEntryExists)

	list, err := repo.ListByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "deepseek-v4", list[0].ModelID, "sorted by sort order")
	got := list[1]
	require.Equal(t, full.ModelID, got.ModelID)
	require.Equal(t, full.Name, got.Name)
	require.Equal(t, full.Description, got.Description)
	require.True(t, got.Enabled)
	require.Equal(t, full.Inputs, got.Inputs)
	require.True(t, got.Tools)
	require.Equal(t, full.Context, got.Context)
	require.Equal(t, full.MaxOutput, got.MaxOutput)
	require.Equal(t, full.Reasoning, got.Reasoning)
	require.Equal(t, full.DefaultReasoning, got.DefaultReasoning)
	require.Equal(t, full.Speed, got.Speed)
	require.Equal(t, full.Strengths, got.Strengths)
	require.Equal(t, full.Complexity, got.Complexity)
	require.Equal(t, full.Roles, got.Roles)
	require.Equal(t, full.Use, got.Use)
	require.Equal(t, full.ProfileSource, got.ProfileSource)
	require.Equal(t, full.CostTier, got.CostTier)
	require.Empty(t, list[0].Inputs)

	got.Enabled = false
	got.CostTier = ""
	got.Strengths = nil
	got.Roles = nil
	require.NoError(t, repo.Update(ctx, &got))
	require.NoError(t, repo.UpdateSortOrders(ctx, group.ID, map[int64]int{got.ID: 5, plain.ID: 15}))
	list, err = repo.ListByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-luna", list[0].ModelID)
	require.False(t, list[0].Enabled)
	require.Empty(t, list[0].CostTier)
	require.Empty(t, list[0].Strengths, "cleared fields stay cleared")
	require.Empty(t, list[0].Roles)

	require.ErrorIs(t, repo.UpdateSortOrders(ctx, other.ID, map[int64]int{got.ID: 1}), service.ErrOtohaCatalogEntryNotFound,
		"an entry of another group is not reordered")
	missing := got
	missing.ID = got.ID + 1_000_000
	require.ErrorIs(t, repo.Update(ctx, &missing), service.ErrOtohaCatalogEntryNotFound)

	require.NoError(t, repo.Delete(ctx, plain.ID))
	require.ErrorIs(t, repo.Delete(ctx, plain.ID), service.ErrOtohaCatalogEntryNotFound)

	_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", other.ID)
	require.NoError(t, err)
	list, err = repo.ListByGroup(ctx, other.ID)
	require.NoError(t, err)
	require.Empty(t, list, "a deleted group takes its catalog with it")
}
