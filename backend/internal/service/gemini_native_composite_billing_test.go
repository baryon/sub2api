//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TASK-87: a native generateContent call through the Otoha (composite) group is billed as the same call through a
// Gemini group, and with the counts the Messages path reports (TASK-86): the prompt without its cached part, the cached
// part, and the answer with the thoughts.
func TestGeminiNativeCallThroughACompositeGroupIsBilledLikeAGeminiGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "gemini-3.5-flash"
	usage := `"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":15,"totalTokenCount":180,"thoughtsTokenCount":45,"cachedContentTokenCount":100}`
	pricing := []ChannelModelPricing{{
		Models: []string{model}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(4e-6), CacheReadPrice: testPtrFloat64(0.25e-6),
	}}
	// 20 prompt tokens, 100 cached, 60 answer tokens (15 + 45 thoughts).
	wantTotal := 20*1e-6 + 100*0.25e-6 + 60*4e-6

	for _, stream := range []bool{false, true} {
		action, contentType := "generateContent", "application/json"
		body := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],` + usage + `}`
		if stream {
			action, contentType = "streamGenerateContent", "text/event-stream"
			body = "data: " + body + "\n\n"
		}
		t.Run(action, func(t *testing.T) {
			var costs [2]float64
			for i, platform := range []string{PlatformGemini, PlatformComposite} {
				c, recorder := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/"+model+":"+action, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{contentType}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}}}
				svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
				account := geminiSignalTestAccount()
				ctx := context.Background()
				if platform == PlatformComposite {
					// The route in front of the handler resolves the model to its Gemini side.
					ctx = WithResolvedTargetPlatform(ctx, PlatformGemini)
				}

				result, err := svc.ForwardNative(ctx, c, account, model, action, stream, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, 20, result.Usage.InputTokens)
				require.Equal(t, 100, result.Usage.CacheReadInputTokens)
				require.Equal(t, 60, result.Usage.OutputTokens)

				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				userRepo := &openAIRecordUsageUserRepoStub{}
				billing := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
				billing.resolver = NewModelPricingResolver(nil, billing.billingService)
				groupID := int64(77)
				apiKey := &APIKey{ID: 10, GroupID: &groupID, Group: &Group{
					ID: groupID, Platform: platform, Status: StatusActive, Hydrated: true, RateMultiplier: 1.5, ModelPricing: pricing,
				}}
				require.NoError(t, billing.RecordUsage(ctx, &RecordUsageInput{
					Result: result, APIKey: apiKey, User: &User{ID: 20}, Account: account, QuotaPlatform: QuotaPlatform(ctx, apiKey),
				}))
				require.NotNil(t, usageRepo.lastLog)
				require.Equal(t, 20, usageRepo.lastLog.InputTokens)
				require.Equal(t, 100, usageRepo.lastLog.CacheReadTokens)
				require.Equal(t, 60, usageRepo.lastLog.OutputTokens)
				require.InDelta(t, wantTotal, usageRepo.lastLog.TotalCost, 1e-12, platform)
				require.InDelta(t, wantTotal*1.5, usageRepo.lastLog.ActualCost, 1e-12, platform)
				require.InDelta(t, wantTotal*1.5, userRepo.lastAmount, 1e-12, platform)
				costs[i] = usageRepo.lastLog.ActualCost
			}
			require.Equal(t, costs[0], costs[1], "the composite group is charged what the Gemini group is")
		})
	}
}
