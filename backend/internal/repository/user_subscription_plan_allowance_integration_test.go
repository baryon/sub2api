//go:build integration

package repository

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// TASK-57: a subscription keeps the plan it was bought with and the plan's allowance; Update writes them
// (including clearing an allowance) and the reads return them.
func (s *UserSubscriptionRepoSuite) TestPlanAllowanceRoundTrip() {
	user := s.mustCreateUser("plan-allowance@test.com", service.RoleUser)
	group := s.mustCreateGroup("g-plan-allowance")

	planID := int64(42)
	monthly := 40.0
	daily := 4.5
	now := time.Now()
	sub := &service.UserSubscription{
		UserID:          user.ID,
		GroupID:         group.ID,
		StartsAt:        now,
		ExpiresAt:       now.AddDate(0, 0, 30),
		Status:          service.SubscriptionStatusActive,
		AssignedAt:      now,
		PlanID:          &planID,
		DailyLimitUSD:   &daily,
		MonthlyLimitUSD: &monthly,
	}
	s.Require().NoError(s.repo.Create(s.ctx, sub))

	got, err := s.repo.GetByID(s.ctx, sub.ID)
	s.Require().NoError(err)
	s.Require().NotNil(got.PlanID)
	s.Require().Equal(planID, *got.PlanID)
	s.Require().NotNil(got.MonthlyLimitUSD)
	s.Require().InDelta(40, *got.MonthlyLimitUSD, 1e-9)
	s.Require().NotNil(got.DailyLimitUSD)
	s.Require().InDelta(4.5, *got.DailyLimitUSD, 1e-9)
	s.Require().Nil(got.WeeklyLimitUSD)

	// Upgrade to another plan: a new plan id and allowance, the daily allowance cleared.
	otherPlan := int64(43)
	bigger := 400.0
	got.PlanID = &otherPlan
	got.MonthlyLimitUSD = &bigger
	got.DailyLimitUSD = nil
	s.Require().NoError(s.repo.Update(s.ctx, got))

	active, err := s.repo.GetActiveByUserIDAndGroupID(s.ctx, user.ID, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(otherPlan, *active.PlanID)
	s.Require().InDelta(400, *active.MonthlyLimitUSD, 1e-9)
	s.Require().Nil(active.DailyLimitUSD, "Update clears an allowance the new plan does not set")

	locked, err := s.repo.GetByIDForUpdate(s.ctx, sub.ID)
	s.Require().NoError(err)
	s.Require().Equal(otherPlan, *locked.PlanID)

	list, err := s.repo.ListActiveByUserID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	s.Require().InDelta(400, *list[0].MonthlyLimitUSD, 1e-9)
}

// A subscription created without a plan (admin assignment, redeem code, or before TASK-57) has none of these.
func (s *UserSubscriptionRepoSuite) TestSubscriptionWithoutPlanHasNoAllowance() {
	user := s.mustCreateUser("no-plan-allowance@test.com", service.RoleUser)
	group := s.mustCreateGroup("g-no-plan-allowance")
	created := s.mustCreateSubscription(user.ID, group.ID, nil)

	got, err := s.repo.GetByID(s.ctx, created.ID)
	s.Require().NoError(err)
	s.Require().Nil(got.PlanID)
	s.Require().Nil(got.DailyLimitUSD)
	s.Require().Nil(got.WeeklyLimitUSD)
	s.Require().Nil(got.MonthlyLimitUSD)

	// A renewal through Update keeps it that way.
	got.ExpiresAt = got.ExpiresAt.AddDate(0, 0, 30)
	s.Require().NoError(s.repo.Update(s.ctx, got))
	again, err := s.repo.GetByID(s.ctx, created.ID)
	s.Require().NoError(err)
	s.Require().Nil(again.PlanID)
	s.Require().Nil(again.MonthlyLimitUSD)
}
