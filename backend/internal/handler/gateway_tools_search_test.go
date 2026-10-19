package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type toolsSearchBillingStub struct {
	settled                 int
	reserved                float64
	settleErr, admissionErr error
	beforeSettle            func()
	executions              []service.SearchExecution
}

func (s *toolsSearchBillingStub) QuoteStandaloneSearch(context.Context, *service.APIKey, *service.UserSubscription) (*service.SearchChargeQuote, error) {
	return &service.SearchChargeQuote{AmountUSD: .02, UnitPriceUSD: .01, RateMultiplier: 2, BillingSource: "balance"}, nil
}
func (s *toolsSearchBillingStub) SettleStandaloneSearch(_ context.Context, q *service.SearchChargeQuote, e service.SearchExecution) (*service.SearchCostReceipt, error) {
	s.settled++
	s.executions = append(s.executions, e)
	if s.beforeSettle != nil {
		s.beforeSettle()
	}
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &service.SearchCostReceipt{AmountUSD: q.AmountUSD, Currency: "USD", Calls: 1, RequestID: e.RequestID, BillingSource: q.BillingSource, UnitPriceUSD: q.UnitPriceUSD, RateMultiplier: q.RateMultiplier}, nil
}
func (s *toolsSearchBillingStub) CheckBillingEligibility(context.Context, *service.User, *service.APIKey, *service.Group, *service.UserSubscription, string) error {
	return s.admissionErr
}
func (s *toolsSearchBillingStub) ReserveFixedCharge(_ context.Context, _ *service.User, _ *service.APIKey, _ *service.Group, _ *service.UserSubscription, amount float64) (*service.InflightReservation, error) {
	s.reserved = amount
	return nil, s.admissionErr
}

func TestToolsSearchSettlesBeforeReturningResultsAndReceipt(t *testing.T) {
	billing := &toolsSearchBillingStub{}
	h := &GatewayHandler{toolsSearchBilling: billing, toolsSearchAdmission: billing, toolsSearchRateLimit: func(*gin.Context) {}}
	var seen websearch.SearchRequest
	h.toolsSearchProvider = func(_ context.Context, request websearch.SearchRequest) (*websearch.SearchResponse, string, error) {
		seen = request
		return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.test/page", Title: "A page", Snippet: "short", Summary: "long", Content: "body", SiteName: "Example", Date: "2026-10-18T00:00:00Z"}}}, "deepseek", nil
	}
	router := gin.New()
	router.POST("/v1/tools/search", securityAuditMediaTestMiddleware, h.ToolsSearch)
	for range 2 {
		response := httptest.NewRecorder()
		billing.beforeSettle = func() {
			require.Empty(t, response.Body.String())
			require.Empty(t, response.Header().Get("X-Otoha-Cost-Receipt"))
		}
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":"q","ori_question":"intent","need_content":true}`)))
		require.Equal(t, http.StatusOK, response.Code)
		require.JSONEq(t, `{"results":[{"url":"https://example.test/page","title":"A page","snippet":"short","summary":"long","content":"body","site_name":"Example","date":"2026-10-18T00:00:00Z","site_icon":null,"rerank_score":0,"authority":-1}]}`, response.Body.String())
		var receipt service.SearchCostReceipt
		require.NoError(t, json.Unmarshal([]byte(response.Header().Get("X-Otoha-Cost-Receipt")), &receipt))
		require.Equal(t, .02, receipt.AmountUSD)
		require.Equal(t, "USD", receipt.Currency)
		require.Equal(t, 1, receipt.Calls)
		require.NotEmpty(t, receipt.RequestID)
	}
	require.Equal(t, .02, billing.reserved)
	require.Equal(t, 2, billing.settled)
	require.NotEqual(t, billing.executions[0].RequestID, billing.executions[1].RequestID)
	require.Equal(t, 10, seen.MaxResults)
	require.Equal(t, "intent", *seen.OriQuestion)
	require.True(t, seen.NeedContent)
}

func TestToolsSearchEmptyAndFailedRequestsNeverGetCharged(t *testing.T) {
	for _, mode := range []string{"empty", "upstream", "settlement", "balance", "rate"} {
		t.Run(mode, func(t *testing.T) {
			billing := &toolsSearchBillingStub{}
			h := &GatewayHandler{toolsSearchBilling: billing, toolsSearchAdmission: billing, toolsSearchRateLimit: func(*gin.Context) {}}
			calls := 0
			h.toolsSearchProvider = func(context.Context, websearch.SearchRequest) (*websearch.SearchResponse, string, error) {
				calls++
				if mode == "upstream" {
					return nil, "", errors.New("private-key-marker")
				}
				if mode == "empty" {
					return &websearch.SearchResponse{}, "deepseek", nil
				}
				return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.test", Snippet: "text"}}}, "brave", nil
			}
			if mode == "settlement" {
				billing.settleErr = errors.New("private-db-marker")
			}
			if mode == "balance" {
				billing.admissionErr = service.ErrInsufficientBalance
			}
			if mode == "rate" {
				h.toolsSearchRateLimit = func(c *gin.Context) { c.AbortWithStatus(http.StatusTooManyRequests) }
			}
			router := gin.New()
			router.POST("/v1/tools/search", securityAuditMediaTestMiddleware, h.ToolsSearch)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":"q"}`)))
			if mode == "empty" {
				require.Equal(t, http.StatusOK, response.Code)
				require.JSONEq(t, `{"results":[]}`, response.Body.String())
				var receipt service.SearchCostReceipt
				require.NoError(t, json.Unmarshal([]byte(response.Header().Get("X-Otoha-Cost-Receipt")), &receipt))
				require.Zero(t, receipt.AmountUSD)
				require.Zero(t, receipt.Calls)
			} else {
				require.GreaterOrEqual(t, response.Code, 400)
				require.Empty(t, response.Header().Get("X-Otoha-Cost-Receipt"))
			}
			if mode != "settlement" {
				require.Zero(t, billing.settled)
			}
			if mode == "balance" {
				require.Equal(t, http.StatusPaymentRequired, response.Code)
			}
			if mode == "balance" || mode == "rate" {
				require.Zero(t, calls)
			}
			require.NotContains(t, response.Body.String(), "private-")
		})
	}
}

func TestToolsSearchAuditsQueryAndOriginalQuestionBeforeBilling(t *testing.T) {
	engine := blockingHandlerPromptEngine()
	h := &GatewayHandler{securityAuditCoordinator: securityaudit.NewCoordinator(nil, engine)}
	router := gin.New()
	router.POST("/v1/tools/search", securityAuditMediaTestMiddleware, h.ToolsSearch)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":"search text","ori_question":"original user intent"}`)))
	require.Equal(t, http.StatusForbidden, response.Code)
	evaluated, _, requests := engine.snapshot()
	require.Equal(t, 1, evaluated)
	require.Len(t, requests, 1)
	serialized := string(requests[0].Body)
	require.Contains(t, serialized, "search text")
	require.Contains(t, serialized, "original user intent")
}

func TestToolsSearchRequestValidation(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"query":"  "}`, `{"query":5}`,
		`{"query":"q","count":0}`, `{"query":"q","count":51}`, `{"query":"q","count":1.5}`,
		`{"query":"q","count":null}`, `{"query":"q","need_content":null}`,
		`{"query":"q","ori_question":""}`, `{"query":"q","ori_question":false}`,
		`{"query":"q"} {}`, `{"query":"` + strings.Repeat("字", 129) + `"}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := decodeToolsSearchRequest(strings.NewReader(body))
			require.Error(t, err)
		})
	}
	request, err := decodeToolsSearchRequest(strings.NewReader(`{"query":"` + strings.Repeat("字", 128) + `","count":50,"ori_question":null,"need_content":true,"future_field":1}`))
	require.NoError(t, err)
	require.Equal(t, 50, *request.Count)
	require.True(t, request.NeedContent)
	require.Nil(t, request.OriQuestion)
}

func TestToolsSearchMapsUpstreamFailuresWithoutLeakingDetails(t *testing.T) {
	for _, status := range []int{402, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			billing := &toolsSearchBillingStub{}
			h := &GatewayHandler{toolsSearchBilling: billing, toolsSearchAdmission: billing, toolsSearchRateLimit: func(*gin.Context) {}}
			h.toolsSearchProvider = func(context.Context, websearch.SearchRequest) (*websearch.SearchResponse, string, error) {
				return nil, "", &websearch.UpstreamError{Provider: "deepseek", StatusCode: status}
			}
			router := gin.New()
			router.POST("/v1/tools/search", securityAuditMediaTestMiddleware, h.ToolsSearch)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":"q"}`)))
			want := http.StatusServiceUnavailable
			if status == 429 {
				want = http.StatusTooManyRequests
				require.Equal(t, "1", response.Header().Get("Retry-After"))
			}
			require.Equal(t, want, response.Code)
			require.Zero(t, billing.settled)
			require.Empty(t, response.Header().Get("X-Otoha-Cost-Receipt"))
			require.NotContains(t, response.Body.String(), "deepseek")
		})
	}
}

func TestToolsSearchFallbackResultsHonorCountAndContentChoice(t *testing.T) {
	results := toolsSearchResults([]websearch.SearchResult{
		{URL: "https://example.test/one", Snippet: "short", Content: "must not escape"},
		{URL: "https://example.test/two"},
	}, 1, false)
	require.Len(t, results, 1)
	require.Equal(t, "", results[0]["content"])
	require.Equal(t, "short", results[0]["summary"])
	require.Nil(t, results[0]["date"])
}

func TestToolsSearchBillingQuotaErrorsRemainRetryable(t *testing.T) {
	for _, err := range []error{service.ErrDailyLimitExceeded, service.ErrWeeklyLimitExceeded, service.ErrMonthlyLimitExceeded, service.ErrAPIKeyQuotaExhausted} {
		router := gin.New()
		router.POST("/t", func(c *gin.Context) { toolsSearchBillingError(c, err) })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/t", nil))
		require.Equal(t, http.StatusTooManyRequests, response.Code)
		require.NotEmpty(t, response.Header().Get("Retry-After"))
	}
}
