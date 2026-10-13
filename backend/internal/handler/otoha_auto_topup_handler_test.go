//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeOtohaAutoTopUp struct {
	state    *service.OtohaAutoTopUp
	err      error
	userID   int64
	settings service.AutoTopUpSettings
	setupID  string
	calls    []string
}

func (f *fakeOtohaAutoTopUp) StateForWebsite(_ context.Context, userID int64) (*service.OtohaAutoTopUp, error) {
	f.userID = userID
	f.calls = append(f.calls, "state")
	return f.state, f.err
}

func (f *fakeOtohaAutoTopUp) UpdateSettings(_ context.Context, userID int64, in service.AutoTopUpSettings) (*service.OtohaAutoTopUp, error) {
	f.userID, f.settings = userID, in
	f.calls = append(f.calls, "update")
	return f.state, f.err
}

func (f *fakeOtohaAutoTopUp) StartCardSetup(_ context.Context, userID int64) (*service.CardSetupStart, error) {
	f.userID = userID
	f.calls = append(f.calls, "start")
	return &service.CardSetupStart{ClientSecret: "seti_secret", PublishableKey: "pk_test"}, f.err
}

func (f *fakeOtohaAutoTopUp) FinishCardSetup(_ context.Context, userID int64, setupID string) (*service.OtohaAutoTopUp, error) {
	f.userID, f.setupID = userID, setupID
	f.calls = append(f.calls, "finish")
	return f.state, f.err
}

func (f *fakeOtohaAutoTopUp) RemoveCard(_ context.Context, userID int64) (*service.OtohaAutoTopUp, error) {
	f.userID = userID
	f.calls = append(f.calls, "remove")
	return f.state, f.err
}

func newOtohaAutoTopUpTestRouter(auto otohaAutoTopUp, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := newOtohaHandler(&fakeOtohaClaims{}, auto)
	r := gin.New()
	auth := func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		}
		c.Next()
	}
	r.GET("/api/v1/otoha/auto-topup", auth, h.GetAutoTopUp)
	r.PUT("/api/v1/otoha/auto-topup", auth, h.UpdateAutoTopUp)
	r.POST("/api/v1/otoha/auto-topup/card-setup", auth, h.StartAutoTopUpCardSetup)
	r.POST("/api/v1/otoha/auto-topup/card", auth, h.FinishAutoTopUpCardSetup)
	r.DELETE("/api/v1/otoha/auto-topup/card", auth, h.RemoveAutoTopUpCard)
	return r
}

func TestOtohaAutoTopUpEndpointsRequireAUserAndAnswerNoStore(t *testing.T) {
	auto := &fakeOtohaAutoTopUp{state: &service.OtohaAutoTopUp{}}
	r := newOtohaAutoTopUpTestRouter(auto, 0)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/otoha/auto-topup"},
		{http.MethodPut, "/api/v1/otoha/auto-topup"},
		{http.MethodPost, "/api/v1/otoha/auto-topup/card-setup"},
		{http.MethodPost, "/api/v1/otoha/auto-topup/card"},
		{http.MethodDelete, "/api/v1/otoha/auto-topup/card"},
	} {
		w := doOtohaRequest(r, tc.method, tc.path, `{}`)
		require.Equal(t, http.StatusUnauthorized, w.Code, tc.method+" "+tc.path)
	}
	require.Empty(t, auto.calls)

	w := doOtohaRequest(newOtohaAutoTopUpTestRouter(auto, 9), http.MethodGet, "/api/v1/otoha/auto-topup", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, int64(9), auto.userID, "only the logged-in user's own settings")
}

func TestOtohaUpdateAutoTopUpPassesTheAmounts(t *testing.T) {
	auto := &fakeOtohaAutoTopUp{state: &service.OtohaAutoTopUp{Enabled: true, AmountUSD: 30}}
	w := doOtohaRequest(newOtohaAutoTopUpTestRouter(auto, 9), http.MethodPut, "/api/v1/otoha/auto-topup",
		`{"enabled":true,"threshold_usd":8,"amount_usd":30,"monthly_cap_usd":120}`)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, service.AutoTopUpSettings{Enabled: true, ThresholdUSD: 8, AmountUSD: 30, MonthlyCapUSD: 120}, auto.settings)
	var resp struct {
		Data service.OtohaAutoTopUp `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Data.Enabled)
	require.Equal(t, 30.0, resp.Data.AmountUSD)
}

func TestOtohaUpdateAutoTopUpRefusesIncompleteOrUnreadableBodies(t *testing.T) {
	auto := &fakeOtohaAutoTopUp{state: &service.OtohaAutoTopUp{}}
	r := newOtohaAutoTopUpTestRouter(auto, 9)
	for _, body := range []string{
		``, `not json`, `{}`,
		`{"enabled":true,"threshold_usd":8,"amount_usd":30}`,
		`{"enabled":true,"threshold_usd":"8","amount_usd":30,"monthly_cap_usd":120}`,
		`{"threshold_usd":8,"amount_usd":30,"monthly_cap_usd":120}`,
	} {
		w := doOtohaRequest(r, http.MethodPut, "/api/v1/otoha/auto-topup", body)
		require.Equal(t, http.StatusBadRequest, w.Code, "body %q", body)
	}
	require.Empty(t, auto.calls, "nothing reaches the service")
}

func TestOtohaAutoTopUpShowsTheServiceReasonWhenItRefuses(t *testing.T) {
	auto := &fakeOtohaAutoTopUp{err: service.ErrAutoTopUpNeedsCard}
	w := doOtohaRequest(newOtohaAutoTopUpTestRouter(auto, 9), http.MethodPut, "/api/v1/otoha/auto-topup",
		`{"enabled":true,"threshold_usd":5,"amount_usd":20,"monthly_cap_usd":100}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "AUTO_TOPUP_NEEDS_CARD")
}

func TestOtohaAutoTopUpCardSetupFlow(t *testing.T) {
	auto := &fakeOtohaAutoTopUp{state: &service.OtohaAutoTopUp{Card: &service.OtohaAutoTopUpCard{Brand: "visa", Last4: "4242"}}}
	r := newOtohaAutoTopUpTestRouter(auto, 9)

	w := doOtohaRequest(r, http.MethodPost, "/api/v1/otoha/auto-topup/card-setup", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"), "the client secret is not cached")
	require.Contains(t, w.Body.String(), "seti_secret")

	w = doOtohaRequest(r, http.MethodPost, "/api/v1/otoha/auto-topup/card", `{"setup_id":"seti_1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "seti_1", auto.setupID)
	require.Contains(t, w.Body.String(), `"last4":"4242"`)

	w = doOtohaRequest(r, http.MethodPost, "/api/v1/otoha/auto-topup/card", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "a card cannot be confirmed without its setup")

	w = doOtohaRequest(r, http.MethodDelete, "/api/v1/otoha/auto-topup/card", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"start", "finish", "remove"}, auto.calls)
}

func TestOtohaAutoTopUpWithoutTheServiceIsUnavailable(t *testing.T) {
	r := newOtohaAutoTopUpTestRouter(nil, 9)
	w := doOtohaRequest(r, http.MethodGet, "/api/v1/otoha/auto-topup", "")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "AUTO_TOPUP_UNAVAILABLE")
}
