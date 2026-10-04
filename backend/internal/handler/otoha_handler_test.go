//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeOtohaClaims struct {
	claim        *service.OtohaClaimCode
	config       *service.OtohaConfigFile
	account      *service.OtohaAccount
	err          error
	redeemedCode string
	userID       int64
}

func (f *fakeOtohaClaims) CreateClaim(_ context.Context, userID int64) (*service.OtohaClaimCode, error) {
	f.userID = userID
	return f.claim, f.err
}

func (f *fakeOtohaClaims) RedeemClaim(_ context.Context, code string) (*service.OtohaConfigFile, error) {
	f.redeemedCode = code
	return f.config, f.err
}

func (f *fakeOtohaClaims) ConfigForUser(_ context.Context, userID int64) (*service.OtohaConfigFile, error) {
	f.userID = userID
	return f.config, f.err
}

func (f *fakeOtohaClaims) AccountSummary(_ context.Context, userID int64) (*service.OtohaAccount, error) {
	f.userID = userID
	return f.account, f.err
}

func otohaTestConfig() *service.OtohaConfigFile {
	return &service.OtohaConfigFile{
		Version: 1,
		Providers: []service.OtohaConfigProvider{{
			Kind: "compatibleGateway", BaseURL: "https://api.otohaai.com", Model: "gpt-6-luna", APIKey: "sk-otoha",
		}},
		Subscription: &service.OtohaConfigSubscription{Account: "someone@example.com"},
	}
}

func newOtohaTestRouter(claims otohaClaims, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := newOtohaHandler(claims)
	r := gin.New()
	auth := func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		}
		c.Next()
	}
	r.POST("/api/v1/otoha/claims", auth, h.CreateClaim)
	r.GET("/api/v1/otoha/config", auth, h.DownloadConfig)
	r.GET("/api/v1/otoha/account", auth, h.GetAccount)
	r.POST("/api/v1/otoha/claim", h.RedeemClaim)
	return r
}

func doOtohaRequest(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOtohaRedeemClaimAnswersWithTheBareConfigurationFile(t *testing.T) {
	claims := &fakeOtohaClaims{config: otohaTestConfig()}
	w := doOtohaRequest(newOtohaTestRouter(claims, 0), http.MethodPost, "/api/v1/otoha/claim", `{"code":"ABCDE-FGHJK-MNPQR-STVWX"}`)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ABCDE-FGHJK-MNPQR-STVWX", claims.redeemedCode)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.EqualValues(t, 1, doc["version"], "the body is the config file itself, not wrapped")
	require.NotContains(t, doc, "data")
	require.Equal(t, "sk-otoha", doc["providers"].([]any)[0].(map[string]any)["apiKey"])
}

func TestOtohaRedeemClaimFailsAlikeForBadBodiesAndBadCodes(t *testing.T) {
	claims := &fakeOtohaClaims{err: service.ErrOtohaClaimInvalid}
	r := newOtohaTestRouter(claims, 0)

	var bodies []string
	for _, body := range []string{`{"code":"ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ"}`, `not json`, `{}`, `{"code":42}`} {
		w := doOtohaRequest(r, http.MethodPost, "/api/v1/otoha/claim", body)
		require.Equal(t, http.StatusBadRequest, w.Code, "body %q", body)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.Contains(t, w.Body.String(), "OTOHA_CLAIM_INVALID")
		bodies = append(bodies, w.Body.String())
	}
	for _, b := range bodies {
		require.Equal(t, bodies[0], b)
	}
}

func TestOtohaRedeemClaimHidesInternalErrors(t *testing.T) {
	claims := &fakeOtohaClaims{err: errors.New("pq: connection refused at 10.0.0.5")}
	w := doOtohaRequest(newOtohaTestRouter(claims, 0), http.MethodPost, "/api/v1/otoha/claim", `{"code":"ABCDE-FGHJK-MNPQR-STVWX"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "10.0.0.5")
}

func TestOtohaCreateClaimReturnsTheCodeForTheLoggedInUser(t *testing.T) {
	expires := time.Date(2026, 10, 4, 8, 15, 0, 0, time.UTC)
	claims := &fakeOtohaClaims{claim: &service.OtohaClaimCode{Code: "ABCDE-FGHJK-MNPQR-STVWX", ExpiresAt: expires, OpenURL: "otoha://claim?code=ABCDE-FGHJK-MNPQR-STVWX"}}
	w := doOtohaRequest(newOtohaTestRouter(claims, 9), http.MethodPost, "/api/v1/otoha/claims", "")

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(9), claims.userID)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Code      string    `json:"code"`
			ExpiresAt time.Time `json:"expires_at"`
			OpenURL   string    `json:"open_url"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "ABCDE-FGHJK-MNPQR-STVWX", resp.Data.Code)
	require.Equal(t, expires, resp.Data.ExpiresAt)
	require.Equal(t, "otoha://claim?code=ABCDE-FGHJK-MNPQR-STVWX", resp.Data.OpenURL)
}

func TestOtohaCreateClaimExplainsWhyItCannot(t *testing.T) {
	claims := &fakeOtohaClaims{err: service.ErrOtohaNoAccess}
	w := doOtohaRequest(newOtohaTestRouter(claims, 9), http.MethodPost, "/api/v1/otoha/claims", "")
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "OTOHA_NO_ACCESS")
}

func TestOtohaLoggedInEndpointsRequireAUser(t *testing.T) {
	claims := &fakeOtohaClaims{config: otohaTestConfig(), account: &service.OtohaAccount{}}
	r := newOtohaTestRouter(claims, 0)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/otoha/claims"},
		{http.MethodGet, "/api/v1/otoha/config"},
		{http.MethodGet, "/api/v1/otoha/account"},
	} {
		w := doOtohaRequest(r, tc.method, tc.path, "")
		require.Equal(t, http.StatusUnauthorized, w.Code, tc.path)
	}
	require.Zero(t, claims.userID)
}

func TestOtohaDownloadConfigIsAnAttachment(t *testing.T) {
	claims := &fakeOtohaClaims{config: otohaTestConfig()}
	w := doOtohaRequest(newOtohaTestRouter(claims, 9), http.MethodGet, "/api/v1/otoha/config", "")

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(9), claims.userID)
	require.Equal(t, `attachment; filename="config.json"`, w.Header().Get("Content-Disposition"))
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	var doc service.OtohaConfigFile
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Equal(t, 1, doc.Version)
	require.Equal(t, "sk-otoha", doc.Providers[0].APIKey)
}

func TestOtohaGetAccountWrapsTheSummary(t *testing.T) {
	claims := &fakeOtohaClaims{account: &service.OtohaAccount{GroupID: 7, Email: "someone@example.com", Balance: 3}}
	w := doOtohaRequest(newOtohaTestRouter(claims, 9), http.MethodGet, "/api/v1/otoha/account", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data service.OtohaAccount `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, int64(7), resp.Data.GroupID)
	require.Equal(t, 3.0, resp.Data.Balance)
}
