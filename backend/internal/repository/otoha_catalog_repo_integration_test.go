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
		API: "deepseek-responses",
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
	require.Equal(t, full.API, got.API)
	require.Empty(t, list[0].Inputs)
	require.Empty(t, list[0].API, "an entry without a chosen format derives it")

	got.Enabled = false
	got.CostTier = ""
	got.API = "openai-responses"
	got.Strengths = nil
	got.Roles = nil
	require.NoError(t, repo.Update(ctx, &got))
	require.NoError(t, repo.UpdateSortOrders(ctx, group.ID, map[int64]int{got.ID: 5, plain.ID: 15}))
	list, err = repo.ListByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-luna", list[0].ModelID)
	require.False(t, list[0].Enabled)
	require.Empty(t, list[0].CostTier)
	require.Equal(t, "openai-responses", list[0].API)
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

// An image entry keeps who made it, the fields the admin changed and when the upstream stopped listing it.
func TestOtohaCatalogImageRepositoryKeepsDiscoveryState(t *testing.T) {
	ctx := context.Background()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("otoha-catalog-images-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM otoha_catalog_image_models WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
	})
	repo := NewOtohaCatalogImageRepository(integrationEntClient)

	manual := &service.OtohaImageEntry{GroupID: group.ID, ModelID: "gpt-image-1", Name: "by hand", Enabled: true, Sizes: []string{"1024x1024"}, SortOrder: 10}
	require.NoError(t, repo.CreateImage(ctx, manual))
	require.Equal(t, service.OtohaImageSourceManual, manual.Source, "an entry without a source is the admin's")
	require.Empty(t, manual.ManualFields)
	require.Nil(t, manual.UnavailableSince)

	auto := &service.OtohaImageEntry{
		GroupID: group.ID, ModelID: "gpt-image-2", Name: "GPT Image 2", Enabled: true, Sizes: []string{"1024x1024"}, SortOrder: 20,
		Source: service.OtohaImageSourceAuto, ManualFields: []string{"name"},
	}
	require.NoError(t, repo.CreateImage(ctx, auto))

	since := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	auto.UnavailableSince = &since
	auto.ManualFields = []string{"name", "sizes"}
	require.NoError(t, repo.UpdateImage(ctx, auto))

	list, err := repo.ListImagesByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, service.OtohaImageSourceAuto, list[1].Source)
	require.Equal(t, []string{"name", "sizes"}, list[1].ManualFields)
	require.NotNil(t, list[1].UnavailableSince)
	require.True(t, since.Equal(*list[1].UnavailableSince))

	auto.UnavailableSince = nil
	require.NoError(t, repo.UpdateImage(ctx, auto))
	list, err = repo.ListImagesByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Nil(t, list[1].UnavailableSince, "back in the upstream list")
}

// TASK-77: the tiers billing charged for the outputs of a group's image requests, from its usage log.
func TestOtohaCatalogImageRepositoryReadsBilledOutputTiers(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "otoha-billed-tiers", Platform: service.PlatformOpenAI})
	group := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("otoha-billed-tiers-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	other := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("otoha-billed-tiers-other-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", group.ID, other.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})

	now := time.Now()
	n := 0
	insert := func(groupID int64, model string, requestedModel *string, at time.Time, tier, source, input, output *string) {
		t.Helper()
		n++
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
   (user_id, api_key_id, account_id, group_id, request_id, model, requested_model, image_count, image_size, image_size_source, image_input_size, image_output_size, created_at)
   VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$10,$11,$12)`,
			user.ID, key.ID, account.ID, groupID, fmt.Sprintf("billed-tier-%d-%d", user.ID, n), model, requestedModel, tier, source, input, output, at)
		require.NoError(t, err)
	}
	s := func(v string) *string { return &v }
	days := func(d int) time.Time { return now.AddDate(0, 0, -d) }

	// gpt-image-2 asked 1024x1024 comes back 1254x1254 (2K) twice, and once 3840x2160 (4K).
	insert(group.ID, "gpt-image-2", s("gpt-image-2"), days(1), s("2K"), s("output"), s("1024x1024"), s("1254x1254"))
	insert(group.ID, "gpt-image-2", s("gpt-image-2"), days(2), s("2K"), s("output"), s("1024x1024"), s("1254x1254"))
	insert(group.ID, "gpt-image-2", nil, days(3), s("4K"), s("output"), s("1024x1024"), s("3840x2160"))
	// not counted: too old, billed by the size asked for, mixed tiers, no sizes recorded, another group
	insert(group.ID, "gpt-image-2", s("gpt-image-2"), days(31), s("4K"), s("output"), s("1536x1024"), s("3840x2160"))
	insert(group.ID, "gpt-image-2", s("gpt-image-2"), days(1), s("2K"), s("input"), s("1536x1024"), nil)
	insert(group.ID, "gpt-image-2", s("gpt-image-2"), days(1), s("mixed"), s("output"), s("1536x1024"), s("1254x1254"))
	insert(group.ID, "gpt-image-2", s("gpt-image-2"), days(1), s("2K"), s("output"), nil, s("1254x1254"))
	insert(other.ID, "gpt-image-2", s("gpt-image-2"), days(1), s("4K"), s("output"), s("1024x1024"), s("3840x2160"))
	// a model that was mapped: the name the client used is what the catalog lists
	insert(group.ID, "upstream-name", s("grok-imagine-image"), days(2), s("1K"), s("output"), s("1024X1024"), s("1024x1024"))

	repo := NewOtohaCatalogImageRepository(client)
	got, err := repo.(service.OtohaImageObservations).ObservedImageOutputs(ctx, group.ID, now.AddDate(0, 0, -30))
	require.NoError(t, err)
	require.Equal(t, []service.OtohaImageObservation{
		{Model: "gpt-image-2", RequestedSize: "1024x1024", Tier: "2K", OutputSize: "1254x1254"},
		{Model: "grok-imagine-image", RequestedSize: "1024x1024", Tier: "1K", OutputSize: "1024x1024"},
		{Model: "gpt-image-2", RequestedSize: "1024x1024", Tier: "4K", OutputSize: "3840x2160"},
	}, got, "one row per model, size, tier and output size, most recent first, with the model as the client named it")

	empty, err := repo.(service.OtohaImageObservations).ObservedImageOutputs(ctx, group.ID, now.Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, empty)
}
