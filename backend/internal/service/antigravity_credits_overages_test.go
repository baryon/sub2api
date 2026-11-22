//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/stretchr/testify/require"
)

func TestClassifyAntigravity429(t *testing.T) {
	t.Run("明确配额耗尽", func(t *testing.T) {
		body := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`)
		require.Equal(t, antigravity429QuotaExhausted, classifyAntigravity429(body))
	})

	t.Run("结构化限流", func(t *testing.T) {
		body := []byte(`{
			"error": {
				"status": "RESOURCE_EXHAUSTED",
				"details": [
					{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
					{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
				]
			}
		}`)
		require.Equal(t, antigravity429RateLimited, classifyAntigravity429(body))
	})

	t.Run("未知429", func(t *testing.T) {
		body := []byte(`{"error":{"message":"too many requests"}}`)
		require.Equal(t, antigravity429Unknown, classifyAntigravity429(body))
	})
}

func TestIsCreditsExhausted_UsesAICreditsKey(t *testing.T) {
	t.Run("无 AICredits key 则积分可用", func(t *testing.T) {
		account := &Account{
			ID:       1,
			Platform: PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
			},
		}
		require.False(t, account.isCreditsExhausted())
	})

	t.Run("AICredits key 生效则积分耗尽", func(t *testing.T) {
		account := &Account{
			ID:       2,
			Platform: PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
				modelRateLimitsKey: map[string]any{
					creditsExhaustedKey: map[string]any{
						"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
						"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		require.True(t, account.isCreditsExhausted())
	})

	t.Run("AICredits key 过期则积分可用", func(t *testing.T) {
		account := &Account{
			ID:       3,
			Platform: PlatformAntigravity,
			Extra: map[string]any{
				"allow_overages": true,
				modelRateLimitsKey: map[string]any{
					creditsExhaustedKey: map[string]any{
						"rate_limited_at":     time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339),
						"rate_limit_reset_at": time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		require.False(t, account.isCreditsExhausted())
	})
}

func TestHandleSmartRetry_QuotaExhausted_UsesCreditsAndStoresIndependentState(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}
	repo := &stubAntigravityAccountRepo{}
	account := &Account{
		ID:       101,
		Name:     "acc-101",
		Type:     AccountTypeOAuth,
		Platform: PlatformAntigravity,
		Extra: map[string]any{
			"allow_overages": true,
		},
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "claude-sonnet-4-5",
			},
		},
	}

	respBody := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	params := antigravityRetryLoopParams{
		ctx:            context.Background(),
		prefix:         "[test]",
		account:        account,
		accessToken:    "token",
		action:         "generateContent",
		body:           []byte(`{"model":"claude-opus-4-6","request":{}}`),
		httpUpstream:   upstream,
		accountRepo:    repo,
		requestedModel: "claude-opus-4-6",
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	}

	svc := &AntigravityGatewayService{}
	result := svc.handleSmartRetry(params, resp, respBody, "https://ag-1.test", 0, []string{"https://ag-1.test"})

	require.NotNil(t, result)
	require.Equal(t, smartRetryActionBreakWithResp, result.action)
	require.NotNil(t, result.resp)
	require.Nil(t, result.switchError)
	require.Len(t, upstream.requestBodies, 1)
	require.Contains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
	require.Empty(t, repo.modelRateLimitCalls, "overages 成功后不应写入普通 model_rate_limits")
}

func TestHandleSmartRetry_RateLimited_DoesNotUseCredits(t *testing.T) {
	successResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	upstream := &mockSmartRetryUpstream{
		responses: []*http.Response{successResp},
		errors:    []error{nil},
	}
	repo := &stubAntigravityAccountRepo{}
	account := &Account{
		ID:       102,
		Name:     "acc-102",
		Type:     AccountTypeOAuth,
		Platform: PlatformAntigravity,
		Extra: map[string]any{
			"allow_overages": true,
		},
	}

	respBody := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.1s"}
			]
		}
	}`)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	params := antigravityRetryLoopParams{
		ctx:          context.Background(),
		prefix:       "[test]",
		account:      account,
		accessToken:  "token",
		action:       "generateContent",
		body:         []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		httpUpstream: upstream,
		accountRepo:  repo,
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	}

	svc := &AntigravityGatewayService{}
	result := svc.handleSmartRetry(params, resp, respBody, "https://ag-1.test", 0, []string{"https://ag-1.test"})

	require.NotNil(t, result)
	require.Equal(t, smartRetryActionBreakWithResp, result.action)
	require.NotNil(t, result.resp)
	require.Len(t, upstream.requestBodies, 1)
	require.NotContains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
	require.Empty(t, repo.extraUpdateCalls)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestAntigravityRetryLoop_ModelRateLimited_InjectsCredits(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			},
		},
		errors: []error{nil},
	}
	// 模型已限流 + overages 启用 + 无 AICredits key → 应直接注入积分
	account := &Account{
		ID:          103,
		Name:        "acc-103",
		Type:        AccountTypeOAuth,
		Platform:    PlatformAntigravity,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"allow_overages": true,
			modelRateLimitsKey: map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
		},
	}

	svc := &AntigravityGatewayService{}
	result, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:            context.Background(),
		prefix:         "[test]",
		account:        account,
		accessToken:    "token",
		action:         "generateContent",
		body:           []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		httpUpstream:   upstream,
		requestedModel: "claude-sonnet-4-5",
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requestBodies, 1)
	require.Contains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
}

func TestAntigravityRetryLoop_CreditsExhausted_DoesNotInject(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	// 模型限流 + overages 启用 + AICredits key 生效 → 不应注入积分，应切号
	account := &Account{
		ID:          104,
		Name:        "acc-104",
		Type:        AccountTypeOAuth,
		Platform:    PlatformAntigravity,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"allow_overages": true,
			modelRateLimitsKey: map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
				},
				creditsExhaustedKey: map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
				},
			},
		},
	}

	svc := &AntigravityGatewayService{}
	_, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:            context.Background(),
		prefix:         "[test]",
		account:        account,
		accessToken:    "token",
		action:         "generateContent",
		body:           []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		requestedModel: "claude-sonnet-4-5",
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})

	// 模型限流 + 积分耗尽 → 应触发切号错误
	require.Error(t, err)
	var switchErr *AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
}

func TestAntigravityRetryLoop_CreditErrorMarksExhausted(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-1.test"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	repo := &stubAntigravityAccountRepo{}
	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{
			{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Insufficient GOOGLE_ONE_AI credits"}}`)),
			},
		},
		errors: []error{nil},
	}
	// 模型限流 + overages 启用 + 积分可用 → 注入积分但上游返回积分不足
	account := &Account{
		ID:          105,
		Name:        "acc-105",
		Type:        AccountTypeOAuth,
		Platform:    PlatformAntigravity,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"allow_overages": true,
			modelRateLimitsKey: map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     time.Now().UTC().Format(time.RFC3339),
					"rate_limit_reset_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
		},
	}

	svc := &AntigravityGatewayService{accountRepo: repo}
	result, err := svc.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx:            context.Background(),
		prefix:         "[test]",
		account:        account,
		accessToken:    "token",
		action:         "generateContent",
		body:           []byte(`{"model":"claude-sonnet-4-5","request":{}}`),
		httpUpstream:   upstream,
		accountRepo:    repo,
		requestedModel: "claude-sonnet-4-5",
		handleError: func(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult {
			return nil
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	// 验证 AICredits key 已通过 SetModelRateLimit 写入数据库
	require.Len(t, repo.modelRateLimitCalls, 1, "应通过 SetModelRateLimit 写入 AICredits key")
	require.Equal(t, creditsExhaustedKey, repo.modelRateLimitCalls[0].modelKey)
}

func TestShouldMarkCreditsExhausted(t *testing.T) {
	t.Run("reqErr 不为 nil 时不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusForbidden}
		require.False(t, shouldMarkCreditsExhausted(resp, []byte(`{"error":"Insufficient credits"}`), io.ErrUnexpectedEOF))
	})

	t.Run("resp 为 nil 时不标记", func(t *testing.T) {
		require.False(t, shouldMarkCreditsExhausted(nil, []byte(`{"error":"Insufficient credits"}`), nil))
	})

	t.Run("5xx 响应不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusInternalServerError}
		require.False(t, shouldMarkCreditsExhausted(resp, []byte(`{"error":"Insufficient credits"}`), nil))
	})

	t.Run("408 RequestTimeout 不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusRequestTimeout}
		require.False(t, shouldMarkCreditsExhausted(resp, []byte(`{"error":"Insufficient credits"}`), nil))
	})

	t.Run("Resource has been exhausted 应标记为积分耗尽", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusTooManyRequests}
		body := []byte(`{"error":{"message":"Resource has been exhausted"}}`)
		require.True(t, shouldMarkCreditsExhausted(resp, body, nil))
	})

	t.Run("Resource has been exhausted (check quota) 完整格式应标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusTooManyRequests}
		body := []byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`)
		require.True(t, shouldMarkCreditsExhausted(resp, body, nil))
	})

	t.Run("结构化限流不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusTooManyRequests}
		body := []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.5s"}]}}`)
		require.False(t, shouldMarkCreditsExhausted(resp, body, nil))
	})

	t.Run("含 credits 关键词时标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusForbidden}
		for _, keyword := range []string{
			"Insufficient GOOGLE_ONE_AI credits",
			"insufficient credit balance",
			"not enough credits for this request",
			"Credits exhausted",
			"minimumCreditAmountForUsage requirement not met",
		} {
			body := []byte(`{"error":{"message":"` + keyword + `"}}`)
			require.True(t, shouldMarkCreditsExhausted(resp, body, nil), "should mark for keyword: %s", keyword)
		}
	})

	t.Run("无 credits 关键词时不标记", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusForbidden}
		body := []byte(`{"error":{"message":"permission denied"}}`)
		require.False(t, shouldMarkCreditsExhausted(resp, body, nil))
	})
}

func TestInjectEnabledCreditTypes(t *testing.T) {
	t.Run("正常 JSON 注入成功", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4-5","request":{}}`)
		result := injectEnabledCreditTypes(body)
		require.NotNil(t, result)
		require.Contains(t, string(result), `"enabledCreditTypes"`)
		require.Contains(t, string(result), `GOOGLE_ONE_AI`)
	})

	t.Run("非法 JSON 返回 nil", func(t *testing.T) {
		require.Nil(t, injectEnabledCreditTypes([]byte(`not json`)))
	})

	t.Run("空 body 返回 nil", func(t *testing.T) {
		require.Nil(t, injectEnabledCreditTypes([]byte{}))
	})

	t.Run("已有 enabledCreditTypes 会被覆盖", func(t *testing.T) {
		body := []byte(`{"enabledCreditTypes":["OLD"],"model":"test"}`)
		result := injectEnabledCreditTypes(body)
		require.NotNil(t, result)
		require.Contains(t, string(result), `GOOGLE_ONE_AI`)
		require.NotContains(t, string(result), `OLD`)
	})
}

func TestClearCreditsExhausted(t *testing.T) {
	t.Run("account 为 nil 不操作", func(t *testing.T) {
		repo := &stubAntigravityAccountRepo{}
		svc := &AntigravityGatewayService{accountRepo: repo}
		svc.clearCreditsExhausted(context.Background(), nil)
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("Extra 为 nil 不操作", func(t *testing.T) {
		repo := &stubAntigravityAccountRepo{}
		svc := &AntigravityGatewayService{accountRepo: repo}
		svc.clearCreditsExhausted(context.Background(), &Account{ID: 1})
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("无 modelRateLimitsKey 不操作", func(t *testing.T) {
		repo := &stubAntigravityAccountRepo{}
		svc := &AntigravityGatewayService{accountRepo: repo}
		svc.clearCreditsExhausted(context.Background(), &Account{
			ID:    1,
			Extra: map[string]any{"some_key": "value"},
		})
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("无 AICredits key 不操作", func(t *testing.T) {
		repo := &stubAntigravityAccountRepo{}
		svc := &AntigravityGatewayService{accountRepo: repo}
		svc.clearCreditsExhausted(context.Background(), &Account{
			ID: 1,
			Extra: map[string]any{
				modelRateLimitsKey: map[string]any{
					"claude-sonnet-4-5": map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": "2099-03-15T00:00:00Z",
					},
				},
			},
		})
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("有 AICredits key 时删除并调用 UpdateExtra", func(t *testing.T) {
		repo := &stubAntigravityAccountRepo{}
		svc := &AntigravityGatewayService{accountRepo: repo}
		account := &Account{
			ID: 1,
			Extra: map[string]any{
				modelRateLimitsKey: map[string]any{
					"claude-sonnet-4-5": map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": "2099-03-15T00:00:00Z",
					},
					creditsExhaustedKey: map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		svc.clearCreditsExhausted(context.Background(), account)
		require.Len(t, repo.extraUpdateCalls, 1)
		// AICredits key 应被删除
		rawLimits := account.Extra[modelRateLimitsKey].(map[string]any)
		_, exists := rawLimits[creditsExhaustedKey]
		require.False(t, exists, "AICredits key 应被删除")
		// 普通模型限流应保留
		_, exists = rawLimits["claude-sonnet-4-5"]
		require.True(t, exists, "普通模型限流应保留")
	})
}

func TestAntigravityRetryLoop_ExplicitQuotaExhaustion(t *testing.T) {
	setupBare429Test(t)

	for _, tc := range []struct {
		name         string
		allowCredits bool
		preLimited   bool
		longReset    bool
		creditStatus int
		wantCalls    int
	}{
		{name: "credits disabled", wantCalls: 1},
		{name: "long absolute reset is capped", wantCalls: 1, longReset: true},
		{name: "credits fail", allowCredits: true, creditStatus: http.StatusTooManyRequests, wantCalls: 2},
		{name: "credits succeed", allowCredits: true, creditStatus: http.StatusOK, wantCalls: 2},
		{name: "first request already uses credits", allowCredits: true, preLimited: true, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			resetAt := now.Add(10 * time.Minute)
			if tc.longReset {
				resetAt = now.Add(24 * time.Hour)
			}
			quota := `{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXHAUSTED","metadata":{"quotaResetTimeStamp":"` + resetAt.Format(time.RFC3339) + `"}}]}}`
			account := bare429Account(103)
			account.Type = AccountTypeOAuth
			account.Extra = map[string]any{"allow_overages": tc.allowCredits}
			if tc.preLimited {
				account.Extra[modelRateLimitsKey] = map[string]any{
					"gemini-4-argon-high": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)},
				}
			}
			upstream := &queuedHTTPUpstreamStub{}
			for i := 0; i < antigravityMaxRetries*2; i++ {
				status := http.StatusTooManyRequests
				if i == 1 && tc.creditStatus != 0 {
					status = tc.creditStatus
				}
				upstream.responses = append(upstream.responses, &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader(quota)),
				})
			}

			repo := &stubAntigravityAccountRepo{}
			svc := &AntigravityGatewayService{accountRepo: repo}
			params := newRetryParams(account, upstream, svc.handleUpstreamError)
			params.ctx = ctxWithSingleAccountRetry()
			params.accountRepo = repo
			params.requestedModel = "gemini-4-argon-high"
			result, err := svc.antigravityRetryLoop(params)
			require.Equal(t, tc.wantCalls, upstream.callCount)
			if tc.allowCredits {
				require.Contains(t, string(upstream.requestBodies[tc.wantCalls-1]), "enabledCreditTypes")
			}
			if tc.creditStatus == http.StatusOK {
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, result.resp.StatusCode)
				require.NoError(t, result.resp.Body.Close())
				require.Empty(t, repo.modelRateLimitCalls)
				return
			}
			var switchErr *AntigravityAccountSwitchError
			require.ErrorAs(t, err, &switchErr)
			require.Equal(t, account.ID, switchErr.OriginalAccountID)
			require.Equal(t, http.StatusTooManyRequests, switchErr.statusCode())
			require.Equal(t, params.requestedModel, switchErr.RateLimitedModel)
			require.NotEmpty(t, repo.modelRateLimitCalls)
			require.Equal(t, switchErr.RateLimitedModel, repo.modelRateLimitCalls[0].modelKey)
			if tc.longReset {
				resetAt = now.Add(2 * time.Hour)
			}
			require.WithinDuration(t, resetAt, repo.modelRateLimitCalls[0].resetAt, time.Second)
		})
	}
}

func TestAntigravityForwardAsResponses_QuotaExhaustionReturns429Failover(t *testing.T) {
	setupBare429Test(t)
	quotaBody := `{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXHAUSTED","metadata":{"quotaResetDelay":"10m"}}]}}`
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": {"600"}},
		Body:       io.NopCloser(strings.NewReader(quotaBody)),
	}}}
	svc := newAntigravityCompatService(config.GatewayConfig{}, upstream)
	svc.accountRepo = &stubAntigravityAccountRepo{}
	account := newAntigravityCompatAccount(AccountTypeOAuth)
	body := []byte(`{"model":"gemini-3.1-pro-high","stream":false,"input":"hello"}`)
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)

	_, err := svc.ForwardAsResponses(c.Request.Context(), c, account, body, nil)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, 1, upstream.callCount)
	require.Equal(t, "600", failoverErr.ResponseHeaders.Get("Retry-After"))
	require.JSONEq(t, quotaBody, string(failoverErr.ResponseBody))
}
