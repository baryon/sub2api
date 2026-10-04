//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type planNamesStub map[int64]string

func (s planNamesStub) PlanName(_ context.Context, id int64) (string, error) {
	if name, ok := s[id]; ok {
		return name, nil
	}
	return "", errors.New("no such plan")
}

// TASK-60: a key whose subscription was bought as a plan reads that plan's name; one assigned without a plan,
// or whose plan cannot be read, reads the group's name as before.
func TestUsagePlanNameIsTheSubscriptionsPlan(t *testing.T) {
	planID, gone := int64(1), int64(9)
	cases := []struct {
		plan *int64
		want string
	}{{&planID, "Plus"}, {nil, "Otoha"}, {&gone, "Otoha"}}
	for _, tc := range cases {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		start := time.Now().Add(-24 * time.Hour)
		c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{StartsAt: start, ExpiresAt: start.Add(30 * 24 * time.Hour), MonthlyWindowStart: &start, PlanID: tc.plan})
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 31, Status: service.StatusActive, Group: &service.Group{
			ID: 5, Name: "Otoha", SubscriptionType: service.SubscriptionTypeSubscription, BalanceFallbackEnabled: true,
		}})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		h := &GatewayHandler{
			userService:  service.NewUserService(usageUserRepoStub{balance: 3}, nil, nil, nil),
			usageService: service.NewUsageService(&otohaUsageRepoStub{}, nil, nil, nil),
		}
		h.SetPlanNames(planNamesStub{1: "Plus"})
		h.Usage(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var response map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.Equal(t, tc.want, response["planName"])
	}
}
