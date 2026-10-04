//go:build unit

package service

import (
	"context"
	"math"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// TASK-57: the admin sets each plan's own daily, weekly and monthly allowance. Empty (or 0) means the plan
// follows the group's limit; an update leaves an allowance it does not mention unchanged and clears one sent
// as 0.

func planAllowanceRequest(groupID int64) CreatePlanRequest {
	return CreatePlanRequest{GroupID: groupID, Name: "Pro", Price: 50, ValidityDays: 30, ValidityUnit: "day", ForSale: true}
}

func TestCreatePlanStoresItsOwnAllowance(t *testing.T) {
	ctx := context.Background()
	svc := &PaymentConfigService{entClient: newPaymentConfigServiceTestClient(t)}

	req := planAllowanceRequest(7)
	req.MonthlyLimitUSD = allowanceFloat(120)
	req.DailyLimitUSD = allowanceFloat(0)
	plan, err := svc.CreatePlan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, plan.MonthlyLimitUsd)
	require.InDelta(t, 120, *plan.MonthlyLimitUsd, 1e-9)
	require.Nil(t, plan.DailyLimitUsd, "0 means no allowance of its own")
	require.Nil(t, plan.WeeklyLimitUsd)

	plain, err := svc.CreatePlan(ctx, planAllowanceRequest(7))
	require.NoError(t, err)
	require.Nil(t, plain.MonthlyLimitUsd, "a plan created as before has no allowance of its own")
}

func TestCreatePlanRejectsAnInvalidAllowance(t *testing.T) {
	ctx := context.Background()
	svc := &PaymentConfigService{entClient: newPaymentConfigServiceTestClient(t)}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		req := planAllowanceRequest(7)
		req.WeeklyLimitUSD = allowanceFloat(bad)
		_, err := svc.CreatePlan(ctx, req)
		require.Error(t, err, "weekly %v", bad)
		require.Equal(t, "PLAN_LIMIT_INVALID", infraerrors.Reason(err))
	}
}

func TestUpdatePlanChangesOnlyTheAllowancesItIsGiven(t *testing.T) {
	ctx := context.Background()
	svc := &PaymentConfigService{entClient: newPaymentConfigServiceTestClient(t)}
	req := planAllowanceRequest(7)
	req.DailyLimitUSD = allowanceFloat(10)
	req.MonthlyLimitUSD = allowanceFloat(120)
	plan, err := svc.CreatePlan(ctx, req)
	require.NoError(t, err)

	updated, err := svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{Name: strPtrForPlanTest("Pro+")})
	require.NoError(t, err)
	require.InDelta(t, 120, *updated.MonthlyLimitUsd, 1e-9, "not mentioned: unchanged")
	require.InDelta(t, 10, *updated.DailyLimitUsd, 1e-9)

	updated, err = svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{MonthlyLimitUSD: allowanceFloat(200), DailyLimitUSD: allowanceFloat(0)})
	require.NoError(t, err)
	require.InDelta(t, 200, *updated.MonthlyLimitUsd, 1e-9)
	require.Nil(t, updated.DailyLimitUsd, "0 clears the allowance: the group's limit applies again")

	_, err = svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{MonthlyLimitUSD: allowanceFloat(-5)})
	require.Error(t, err)
	require.Equal(t, "PLAN_LIMIT_INVALID", infraerrors.Reason(err))
}

func TestPlanEffectiveLimitsPreferThePlansOwnAllowance(t *testing.T) {
	group := PlanGroupInfo{DailyLimitUSD: allowanceFloat(5), MonthlyLimitUSD: allowanceFloat(100)}

	own := PlanEffectiveLimits(allowanceFloat(0), nil, allowanceFloat(40), group)
	require.Same(t, group.DailyLimitUSD, own.DailyUSD, "no daily allowance of its own: the group's")
	require.Nil(t, own.WeeklyUSD)
	require.InDelta(t, 40, *own.MonthlyUSD, 1e-9)

	plain := PlanEffectiveLimits(nil, nil, nil, group)
	require.Same(t, group.MonthlyLimitUSD, plain.MonthlyUSD, "a plan without an allowance shows the group's, as before")
}

func strPtrForPlanTest(v string) *string { return &v }
