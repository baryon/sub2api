package admin

import (
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-57: the admin plan list returns the plan's own allowance (plan_*_limit_usd, null when the plan follows
// the group) next to the group's limits, which keep their names.
func TestAdminSubscriptionPlansForResponseIncludesThePlansOwnAllowance(t *testing.T) {
	groupMonthly := 100.0
	planMonthly := 40.0
	plans := []*dbent.SubscriptionPlan{
		{ID: 1, GroupID: 7, Name: "Plus", Price: 20, ValidityDays: 30, MonthlyLimitUsd: &planMonthly},
		{ID: 2, GroupID: 7, Name: "Legacy", Price: 50, ValidityDays: 30},
	}
	groupInfo := map[int64]service.PlanGroupInfo{7: {Name: "Otoha", MonthlyLimitUSD: &groupMonthly}}

	got := adminSubscriptionPlansForResponse(plans, groupInfo)
	require.Len(t, got, 2)
	require.NotNil(t, got[0].PlanMonthlyLimitUSD)
	require.InDelta(t, 40, *got[0].PlanMonthlyLimitUSD, 1e-9)
	require.InDelta(t, 100, *got[0].MonthlyLimitUSD, 1e-9, "the group's limit keeps its field")
	require.Nil(t, got[1].PlanMonthlyLimitUSD)

	body, err := json.Marshal(got[1])
	require.NoError(t, err)
	require.Contains(t, string(body), `"plan_monthly_limit_usd":null`, "the form can tell an empty allowance apart")
	require.Contains(t, string(body), `"plan_daily_limit_usd":null`)
	require.Contains(t, string(body), `"plan_weekly_limit_usd":null`)
}
