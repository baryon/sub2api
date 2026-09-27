package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// For a group whose plan falls back to the balance, /v1/usage tells the app both: the plan's
// credit this period, the balance, and what is left of the two together.
type usageUserRepoStub struct {
	service.UserRepository
	balance float64
}

func (r usageUserRepoStub) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id, Balance: r.balance}, nil
}

func (usageUserRepoStub) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func usageForFallbackGroup(t *testing.T, plan *service.UserSubscription, balance float64, monthlyLimit *float64) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	if plan != nil {
		c.Set(string(middleware.ContextKeySubscription), plan)
	}
	handler := &GatewayHandler{userService: service.NewUserService(usageUserRepoStub{balance: balance}, nil, nil, nil)}
	handler.usageUnrestricted(c, context.Background(),
		&service.APIKey{Group: &service.Group{
			Name: "Otoha", SubscriptionType: service.SubscriptionTypeSubscription, MonthlyLimitUSD: monthlyLimit, BalanceFallbackEnabled: true,
		}},
		middleware.AuthSubject{UserID: 9}, nil, nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestUsageOfABalanceFallbackGroupIncludesPlanAndBalance(t *testing.T) {
	monthly := 20.0
	response := usageForFallbackGroup(t, &service.UserSubscription{MonthlyUsageUSD: 5}, 12, &monthly)

	require.Equal(t, 12.0, response["balance"])
	require.Equal(t, 27.0, response["remaining"], "15 left on the plan and 12 in the balance")
	subscription, ok := response["subscription"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 5.0, subscription["monthly_usage_usd"])
}

func TestUsageOfABalanceFallbackGroupWithoutAPlanIsTheBalance(t *testing.T) {
	monthly := 20.0
	response := usageForFallbackGroup(t, nil, 4.5, &monthly)

	require.Equal(t, 4.5, response["balance"])
	require.Equal(t, 4.5, response["remaining"])
	require.NotContains(t, response, "subscription")
}

// A plan without limits stays without limits: nothing is added to "no limit".
func TestUsageOfABalanceFallbackGroupWithAnUnlimitedPlan(t *testing.T) {
	response := usageForFallbackGroup(t, &service.UserSubscription{MonthlyUsageUSD: 5}, 12, nil)

	require.Equal(t, 12.0, response["balance"])
	require.Equal(t, -1.0, response["remaining"], "the plan's own way of saying unlimited")
}
