//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A group with balance fallback charges the monthly plan while it has credit, then the user's
// balance; only when neither is left is the request refused, with a code the app recognises.
type balanceFallbackCase struct {
	fallback     bool
	subscription *service.UserSubscription // nil: the user has no active plan in the group
	balance      float64
	google       bool   // through the Gemini-style endpoints' middleware
	exclusive    bool   // the group admits only the users it lists; this user is not listed
	lookupErr    error  // looking up the plan fails
	method, path string // another endpoint than a model request
}

func runBalanceFallbackCase(t *testing.T, tc balanceFallbackCase) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	monthly := 20.0
	group := &service.Group{
		ID:                     77,
		Name:                   "otoha",
		Status:                 service.StatusActive,
		Hydrated:               true,
		SubscriptionType:       service.SubscriptionTypeSubscription,
		MonthlyLimitUSD:        &monthly,
		BalanceFallbackEnabled: tc.fallback,
		IsExclusive:            tc.exclusive,
	}
	user := &service.User{ID: 9, Role: service.RoleUser, Status: service.StatusActive, Balance: tc.balance, Concurrency: 3}
	apiKey := &service.APIKey{ID: 300, UserID: user.ID, Key: "otoha-key", Status: service.StatusActive, User: user, Group: group}
	apiKey.GroupID = &group.ID

	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKeyService := service.NewAPIKeyService(&stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*service.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
	}, nil, nil, nil, nil, nil, cfg)
	// Window maintenance may run (a new calendar day starts a new daily window); it keeps the usage.
	plan := tc.subscription
	subscriptionService := service.NewSubscriptionService(nil, &stubUserSubscriptionRepo{
		getActive: func(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
			if tc.lookupErr != nil {
				return nil, tc.lookupErr
			}
			if plan == nil {
				return nil, service.ErrSubscriptionNotFound
			}
			clone := *plan
			return &clone, nil
		},
		getByID: func(ctx context.Context, id int64) (*service.UserSubscription, error) {
			clone := *plan
			return &clone, nil
		},
		updateStatus:   func(ctx context.Context, subscriptionID int64, status string) error { return nil },
		activateWindow: func(ctx context.Context, id int64, dailyStart, periodicStart time.Time) error { return nil },
		resetDaily: func(ctx context.Context, id int64, start time.Time) error {
			plan.DailyWindowStart = &start
			plan.DailyUsageUSD = 0
			return nil
		},
		resetWeekly: func(ctx context.Context, id int64, start time.Time) error {
			plan.WeeklyWindowStart = &start
			return nil
		},
		resetMonthly: func(ctx context.Context, id int64, start time.Time) error {
			plan.MonthlyWindowStart = &start
			return nil
		},
	}, nil, nil, cfg)
	t.Cleanup(subscriptionService.Stop)

	router := gin.New()
	method, path := http.MethodPost, "/v1/responses"
	if tc.path != "" {
		method, path = tc.method, tc.path
	}
	if tc.google {
		router.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
		path = "/v1beta/models/m:generateContent"
	} else {
		router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, subscriptionService, cfg)))
	}
	charged := false
	router.Handle(method, path, func(c *gin.Context) {
		_, charged = c.Get(string(ContextKeySubscription))
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if tc.google {
		req.Header.Set("x-goog-api-key", apiKey.Key)
	} else {
		req.Header.Set("x-api-key", apiKey.Key)
	}
	router.ServeHTTP(w, req)
	return w, charged
}

func activePlan(monthlyUsage float64) *service.UserSubscription {
	start := time.Now().Add(-time.Hour)
	return &service.UserSubscription{
		ID:                 501,
		UserID:             9,
		GroupID:            77,
		Status:             service.SubscriptionStatusActive,
		ExpiresAt:          time.Now().Add(20 * 24 * time.Hour),
		DailyWindowStart:   &start,
		WeeklyWindowStart:  &start,
		MonthlyWindowStart: &start,
		MonthlyUsageUSD:    monthlyUsage,
	}
}

func requireErrorCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, w.Code)
	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, code, resp.Code)
}

func TestBalanceFallbackChargesThePlanWhileItHasCredit(t *testing.T) {
	w, onPlan := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: activePlan(5), balance: 10})

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, onPlan, "the plan pays")
}

func TestBalanceFallbackChargesTheBalanceOnceThePlanIsUsedUp(t *testing.T) {
	w, onPlan := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: activePlan(20.5), balance: 3})

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, onPlan, "the balance pays")
}

func TestBalanceFallbackChargesTheBalanceWithoutAPlan(t *testing.T) {
	w, onPlan := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: nil, balance: 5})

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, onPlan)
}

func TestBalanceFallbackRefusesWhenNeitherPlanNorBalanceIsLeft(t *testing.T) {
	w, _ := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: nil, balance: 0})
	requireErrorCode(t, w, http.StatusPaymentRequired, "CREDIT_EXHAUSTED")

	w, _ = runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: activePlan(20.5), balance: 0})
	requireErrorCode(t, w, http.StatusPaymentRequired, "CREDIT_EXHAUSTED")
}

func TestWithoutBalanceFallbackASubscriptionGroupStillNeedsAPlan(t *testing.T) {
	w, _ := runBalanceFallbackCase(t, balanceFallbackCase{fallback: false, subscription: nil, balance: 5})
	requireErrorCode(t, w, http.StatusForbidden, "SUBSCRIPTION_NOT_FOUND")

	w, _ = runBalanceFallbackCase(t, balanceFallbackCase{fallback: false, subscription: activePlan(20.5), balance: 5})
	requireErrorCode(t, w, http.StatusTooManyRequests, "USAGE_LIMIT_EXCEEDED")
}

func TestBalanceFallbackAlsoHoldsOnTheGeminiStyleEndpoints(t *testing.T) {
	w, onPlan := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: activePlan(20.5), balance: 3, google: true})
	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, onPlan, "the balance pays")

	w, _ = runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: nil, balance: 0, google: true})
	require.Equal(t, http.StatusPaymentRequired, w.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "Plan credit and balance are both used up", resp.Error.Message)
	require.Equal(t, "FAILED_PRECONDITION", resp.Error.Status, "buying credit helps, retrying does not")
}

func TestBalanceFallbackAfterThePlanExpires(t *testing.T) {
	expired := activePlan(0)
	expired.ExpiresAt = time.Now().Add(-time.Hour)

	w, onPlan := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: expired, balance: 5})

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, onPlan, "the balance pays")
}

// A failed lookup is not the same as having no plan: a user with plan credit is not charged the balance
// while the database is unavailable.
func TestBalanceFallbackDoesNotHideAFailedPlanLookup(t *testing.T) {
	w, _ := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, lookupErr: errors.New("database unavailable"), balance: 5})
	requireErrorCode(t, w, http.StatusServiceUnavailable, "BILLING_SERVICE_UNAVAILABLE")

	w, _ = runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, lookupErr: errors.New("database unavailable"), balance: 5, google: true})
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// Paying from the balance is for users the group admits; a plan holder is admitted by the plan.
func TestBalanceFallbackOnlyForUsersTheGroupAdmits(t *testing.T) {
	w, _ := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, exclusive: true, subscription: nil, balance: 5})
	requireErrorCode(t, w, http.StatusForbidden, "GROUP_NOT_ALLOWED")

	w, onPlan := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, exclusive: true, subscription: activePlan(5), balance: 5})
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, onPlan)
}

func TestUsageStaysReadableWhenCreditIsUsedUp(t *testing.T) {
	w, _ := runBalanceFallbackCase(t, balanceFallbackCase{fallback: true, subscription: nil, balance: 0, method: http.MethodGet, path: "/v1/usage"})
	require.Equal(t, http.StatusOK, w.Code)
}
