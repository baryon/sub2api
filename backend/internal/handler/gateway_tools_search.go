package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pkgerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type toolsSearchBilling interface {
	QuoteStandaloneSearch(context.Context, *service.APIKey, *service.UserSubscription) (*service.SearchChargeQuote, error)
	SettleStandaloneSearch(context.Context, *service.SearchChargeQuote, service.SearchExecution) (*service.SearchCostReceipt, error)
}

type toolsSearchProvider func(context.Context, websearch.SearchRequest) (*websearch.SearchResponse, string, error)

type toolsSearchAdmission interface {
	CheckBillingEligibility(context.Context, *service.User, *service.APIKey, *service.Group, *service.UserSubscription, string) error
	ReserveFixedCharge(context.Context, *service.User, *service.APIKey, *service.Group, *service.UserSubscription, float64) (*service.InflightReservation, error)
}

type toolsSearchRequest struct {
	Query        string  `json:"query"`
	Count        *int    `json:"count"`
	OriQuestion  *string `json:"ori_question"`
	NeedContent  bool    `json:"need_content"`
	Localization string  `json:"localization"`
}

func decodeToolsSearchRequest(body io.Reader) (toolsSearchRequest, error) {
	var request toolsSearchRequest
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			return request, sizeError
		}
		return request, errors.New("invalid JSON request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			return request, sizeError
		}
		return request, errors.New("invalid JSON request")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return request, errors.New("query must be supplied in a JSON object")
	}
	for _, field := range []string{"query", "count", "need_content", "localization"} {
		if bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) {
			return request, errors.New(field + " cannot be null")
		}
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return request, errors.New("invalid search field type")
	}
	if strings.TrimSpace(request.Query) == "" || utf8.RuneCountInString(request.Query) > 128 {
		return request, errors.New("query must contain 1 to 128 characters and cannot be blank")
	}
	if request.Count != nil && (*request.Count < 1 || *request.Count > 50) {
		return request, errors.New("count must be between 1 and 50")
	}
	if request.OriQuestion != nil && (utf8.RuneCountInString(*request.OriQuestion) < 1 || utf8.RuneCountInString(*request.OriQuestion) > 128) {
		return request, errors.New("ori_question must contain 1 to 128 characters")
	}
	return request, nil
}

func toolsSearchError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message, "param": nil}})
}

// SetToolsSearchRateLimit wires the shared Redis-backed user throttle at startup.
func (h *GatewayHandler) SetToolsSearchRateLimit(limiter gin.HandlerFunc) {
	h.toolsSearchRateLimit = limiter
}

// ToolsSearch serves search independently of the group's model platform.
func (h *GatewayHandler) ToolsSearch(c *gin.Context) {
	request, err := decodeToolsSearchRequest(c.Request.Body)
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			toolsSearchError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "Search request body is too large")
			return
		}
		toolsSearchError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.User == nil {
		toolsSearchError(c, http.StatusUnauthorized, "authentication_error", "API key required")
		return
	}
	if key.Group == nil || key.GroupID == nil {
		toolsSearchError(c, http.StatusBadRequest, "invalid_request_error", "Group required")
		return
	}
	text := request.Query
	if request.OriQuestion != nil {
		text += "\n" + *request.OriQuestion
	}
	auditBody, _ := json.Marshal(gin.H{"messages": []gin.H{{"role": "user", "content": text}}})
	subject, _ := middleware2.GetAuthSubjectFromContext(c)
	if decision := h.checkSecurityAudit(c, requestLogger(c, "handler.gateway.tools_search"), key, subject,
		service.ContentModerationProtocolOpenAIChat, "web-search", auditBody); decision != nil && !decision.AllowNextStage {
		status := decision.HTTPStatus
		if status < 400 || status > 599 {
			status = http.StatusForbidden
		}
		code, message := decision.ErrorCode, decision.ClientMessage
		if code == "" {
			code = "content_policy_violation"
		}
		if message == "" {
			message = "Request blocked by content policy"
		}
		toolsSearchError(c, status, code, message)
		return
	}
	if h.toolsSearchRateLimit == nil {
		toolsSearchError(c, http.StatusServiceUnavailable, "search_unavailable", "Search is temporarily unavailable")
		return
	}
	h.toolsSearchRateLimit(c)
	if c.IsAborted() {
		return
	}
	billing := h.toolsSearchBilling
	if billing == nil && h.gatewayService != nil {
		billing = h.gatewayService
	}
	admission := h.toolsSearchAdmission
	if admission == nil && h.billingCacheService != nil {
		admission = h.billingCacheService
	}
	if billing == nil || admission == nil {
		toolsSearchError(c, http.StatusServiceUnavailable, "search_unavailable", "Search is temporarily unavailable")
		return
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	ctx := c.Request.Context()
	if err := admission.CheckBillingEligibility(ctx, key.User, key, key.Group, subscription, service.QuotaPlatform(ctx, key)); err != nil {
		toolsSearchBillingError(c, err)
		return
	}
	quote, err := billing.QuoteStandaloneSearch(ctx, key, subscription)
	if err != nil || quote == nil {
		toolsSearchError(c, http.StatusServiceUnavailable, "billing_unavailable", "Search billing is temporarily unavailable")
		return
	}
	reservation, err := admission.ReserveFixedCharge(ctx, key.User, key, key.Group, subscription, quote.AmountUSD)
	if err != nil {
		toolsSearchBillingError(c, err)
		return
	}
	defer reservation.HandlerDone()
	count := 10
	if request.Count != nil {
		count = *request.Count
	}
	search := h.toolsSearchProvider
	if search == nil {
		search = service.SearchTools
	}
	started := time.Now()
	response, provider, err := search(ctx, websearch.SearchRequest{Query: request.Query, MaxResults: count,
		OriQuestion: request.OriQuestion, NeedContent: request.NeedContent, Localization: request.Localization})
	if err != nil || response == nil {
		toolsSearchUpstreamError(c, err)
		return
	}
	results := toolsSearchResults(response.Results, count, request.NeedContent)
	requestID := "tools-search:" + uuid.NewString()
	receipt := &service.SearchCostReceipt{RequestID: requestID, Currency: "USD", BillingSource: "none"}
	if len(results) > 0 {
		payload, _ := json.Marshal(request)
		receipt, err = billing.SettleStandaloneSearch(ctx, quote, service.SearchExecution{
			RequestID: requestID, Provider: provider, PayloadHash: service.HashUsageRequestPayload(payload),
			InboundEndpoint: "/v1/tools/search", UpstreamEndpoint: providerSearchEndpoint(provider),
			UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c), Duration: time.Since(started),
		})
		if err != nil || receipt == nil {
			toolsSearchBillingError(c, err)
			return
		}
		if h.apiKeyService != nil {
			cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			h.apiKeyService.InvalidateAuthCacheByKey(cacheCtx, key.Key)
			cancel()
		}
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		toolsSearchError(c, http.StatusInternalServerError, "billing_unavailable", "Search receipt is unavailable")
		return
	}
	c.Header("X-Otoha-Cost-Receipt", string(receiptJSON))
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"results": results})
}

func toolsSearchBillingError(c *gin.Context, err error) {
	if err == nil || pkgerrors.Code(err) >= 500 {
		toolsSearchError(c, http.StatusServiceUnavailable, "billing_unavailable", "Search billing is temporarily unavailable")
		return
	}
	if errors.Is(err, service.ErrInsufficientBalance) {
		toolsSearchError(c, http.StatusPaymentRequired, "insufficient_balance", "Insufficient balance for search")
		return
	}
	status, code, message, retryAfter := billingErrorDetails(err)
	if pkgerrors.Code(err) == http.StatusTooManyRequests {
		status, code = http.StatusTooManyRequests, "rate_limit_error"
	}
	if status == http.StatusTooManyRequests && retryAfter <= 0 {
		retryAfter = 60
	}
	if retryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(retryAfter))
	}
	toolsSearchError(c, status, code, message)
}

func toolsSearchUpstreamError(c *gin.Context, err error) {
	var upstream *websearch.UpstreamError
	if errors.Is(err, websearch.ErrRateLimited) || (errors.As(err, &upstream) && upstream.StatusCode == http.StatusTooManyRequests) {
		c.Header("Retry-After", "1")
		toolsSearchError(c, http.StatusTooManyRequests, "rate_limit_error", "Search is busy. Please try again shortly")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		toolsSearchError(c, http.StatusGatewayTimeout, "search_timeout", "Search took too long. Please try again")
		return
	}
	// An operator's upstream balance shortage must not be reported as the user's debt.
	toolsSearchError(c, http.StatusServiceUnavailable, "search_unavailable", "Search is temporarily unavailable")
}

func providerSearchEndpoint(provider string) string {
	switch provider {
	case websearch.ProviderTypeDeepSeek:
		return "/v1/tools/search"
	case websearch.ProviderTypeBrave:
		return "/res/v1/web/search"
	case websearch.ProviderTypeTavily:
		return "/search"
	default:
		return ""
	}
}

func toolsSearchResults(results []websearch.SearchResult, count int, needContent bool) []gin.H {
	out := make([]gin.H, 0, min(len(results), count))
	for _, result := range results {
		if len(out) == count {
			break
		}
		summary := result.Summary
		if summary == "" {
			summary = result.Snippet
		}
		content := ""
		if needContent {
			content = result.Content
		}
		score, authority := 0.0, -1
		if result.RerankScore != nil {
			score = *result.RerankScore
		}
		if result.Authority != nil {
			authority = *result.Authority
		}
		out = append(out, gin.H{"title": result.Title, "url": result.URL, "snippet": result.Snippet,
			"summary": summary, "content": content, "site_name": nullableSearchText(result.SiteName),
			"site_icon": nullableSearchText(result.SiteIcon), "date": nullableSearchText(result.Date),
			"rerank_score": score, "authority": authority})
	}
	return out
}

func nullableSearchText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
