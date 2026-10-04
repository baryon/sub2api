//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TASK-64: through one key of a composite group the app calls Claude on /v1/messages (Anthropic gateway), DeepSeek
// on its native Responses and GPT and Grok on OpenAI Responses (OpenAI gateway). Each request is charged what the
// catalog shows: the price of the provider it was routed to, times the group's rate, whether the plan or the balance
// pays. The channel carries wrong prices for each model under another provider, which only a lookup that ignored the
// routed provider would pick.

func otohaCatalogCost(price OtohaModelPrice, input, output int) float64 {
	return (float64(input)*price.Input + float64(output)*price.Output) / 1e6
}

func TestOtohaNativeEndpointsChargeWhatTheCatalogShows(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.diagnoser.models[PlatformDeepseek] = []string{"deepseek-v4-flash"}
	f.diagnoser.models[PlatformGrok] = []string{"grok-4.7"}
	f.add(t, "gpt-6-luna", "claude-sonnet-4-5", "deepseek-v4-flash", "grok-4.7")
	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 4)

	groupID := otohaCompositeGroupID
	const input, output = 1000, 500
	routed := func(platform, model, endpoint string) context.Context {
		return WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
			Matched: true, PublicModel: model, TargetPlatform: platform, UpstreamModel: model, Endpoint: endpoint,
		})
	}

	for _, paidBy := range []string{"balance", "plan"} {
		t.Run(paidBy, func(t *testing.T) {
			group := *f.group
			var subscription *UserSubscription
			if paidBy == "plan" {
				group.SubscriptionType = SubscriptionTypeSubscription
				group.BalanceFallbackEnabled = true
				subscription = &UserSubscription{ID: 31, UserID: 21, GroupID: groupID, Status: SubscriptionStatusActive,
					ExpiresAt: time.Now().Add(24 * time.Hour)}
			}
			apiKey := &APIKey{ID: 11, UserID: 21, GroupID: &groupID, Group: &group}
			user := &User{ID: 21, Balance: 10}

			// Claude through /v1/messages: the Anthropic gateway's billing.
			claude := otohaCatalogModelByID(t, catalog, "claude-sonnet-4-5")
			require.Equal(t, OtohaAPIAnthropicMessages, claude.API)
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			gateway := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)
			gateway.channelService = f.channels
			gateway.resolver = NewModelPricingResolver(f.channels, gateway.billingService)
			require.NoError(t, gateway.RecordUsage(routed(PlatformAnthropic, "claude-sonnet-4-5", CompositeRouteEndpointMessages), &RecordUsageInput{
				Result: &ForwardResult{RequestID: "msg_" + paidBy, Model: "claude-sonnet-4-5",
					Usage: ClaudeUsage{InputTokens: input, OutputTokens: output}, Duration: time.Second},
				APIKey: apiKey, User: user, Subscription: subscription,
				Account:         &Account{ID: 41, Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
				InboundEndpoint: "/v1/messages",
			}))
			require.NotNil(t, usageRepo.lastLog)
			want := otohaCatalogCost(claude.Price, input, output)
			require.InDelta(t, want, usageRepo.lastLog.ActualCost, 1e-12, "Claude on /v1/messages is charged its catalog price")
			otohaRequirePaidBy(t, paidBy, want, userRepo, subRepo)

			// DeepSeek native Responses, GPT and Grok through /v1/responses: the OpenAI gateway's billing.
			for _, tc := range []struct{ model, platform, api string }{
				{"deepseek-v4-flash", PlatformDeepseek, OtohaAPIDeepSeekResponses},
				{"gpt-6-luna", PlatformOpenAI, OtohaAPIOpenAIResponses},
				{"grok-4.7", PlatformGrok, OtohaAPIOpenAIResponses},
			} {
				listed := otohaCatalogModelByID(t, catalog, tc.model)
				require.Equal(t, tc.api, listed.API, tc.model)
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				userRepo := &openAIRecordUsageUserRepoStub{}
				subRepo := &openAIRecordUsageSubRepoStub{}
				openai := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
				openai.channelService = f.channels
				openai.resolver = NewModelPricingResolver(f.channels, openai.billingService)
				require.NoError(t, openai.RecordUsage(routed(tc.platform, tc.model, CompositeRouteEndpointResponses), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "resp_" + tc.model + "_" + paidBy, Model: tc.model,
						Usage: OpenAIUsage{InputTokens: input, OutputTokens: output}, Duration: time.Second},
					APIKey: apiKey, User: user, Subscription: subscription,
					Account:         &Account{ID: 42, Platform: tc.platform, Type: AccountTypeAPIKey},
					InboundEndpoint: "/v1/responses",
				}), tc.model)
				require.NotNil(t, usageRepo.lastLog, tc.model)
				want := otohaCatalogCost(listed.Price, input, output)
				require.InDelta(t, want, usageRepo.lastLog.ActualCost, 1e-12, "%s is charged its catalog price", tc.model)
				otohaRequirePaidBy(t, paidBy, want, userRepo, subRepo)
			}
		})
	}
}

func otohaRequirePaidBy(t *testing.T, paidBy string, want float64, users *openAIRecordUsageUserRepoStub, subs *openAIRecordUsageSubRepoStub) {
	t.Helper()
	if paidBy == "plan" {
		require.Equal(t, 1, subs.incrementCalls, "the plan pays")
		require.Zero(t, users.deductCalls, "the balance is not touched while the plan pays")
		return
	}
	require.Equal(t, 1, users.deductCalls, "the balance pays")
	require.InDelta(t, want, users.lastAmount, 1e-12)
	require.Zero(t, subs.incrementCalls)
}
