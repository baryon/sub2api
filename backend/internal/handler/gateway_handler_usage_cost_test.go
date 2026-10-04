//go:build unit

package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// TASK-58: a key holder reads what their usage cost them, never what the upstream account cost us.
func TestUsageModelStatsLeaveOutTheAccountCost(t *testing.T) {
	repo := &otohaUsageRepoStub{stats: []usagestats.ModelStat{
		{Model: "gpt-6-luna", Requests: 4, InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200, Cost: 0.5, ActualCost: 0.75, AccountCost: 0.2},
	}}
	for _, path := range []string{"/v1/usage", "/v1/usage?client=otoha"} {
		response := otohaUsageRequest(t, path, nil, repo)
		stats, ok := response["model_stats"].([]any)
		require.True(t, ok, "%s: %v", path, response)
		require.Len(t, stats, 1)
		stat := stats[0].(map[string]any)
		require.NotContains(t, stat, "account_cost", path)
		require.Equal(t, "gpt-6-luna", stat["model"])
		require.InDelta(t, 0.75, stat["actual_cost"], 1e-9)
		require.InDelta(t, 0.5, stat["cost"], 1e-9)
		require.EqualValues(t, 1200, stat["total_tokens"])
	}
}
