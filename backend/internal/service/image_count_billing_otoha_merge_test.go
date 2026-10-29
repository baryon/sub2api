//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The count switch must coexist with catalog overrides and carry the resulting
// charge into the atomic billing command, whether the plan or balance pays.
func TestImageCountBillingOtohaMergeRecordUsage(t *testing.T) {
	for _, gateway := range []string{"openai", "anthropic"} {
		for _, catalogOverride := range []bool{false, true} {
			for _, planPays := range []bool{false, true} {
				name := gateway + "/group-price"
				if catalogOverride {
					name = gateway + "/catalog-price"
				}
				if planPays {
					name += "/plan"
				} else {
					name += "/balance-fallback"
				}
				t.Run(name, func(t *testing.T) {
					f := newOtohaImageBillingFixture(t, func(g *Group) {
						g.SubscriptionType = SubscriptionTypeSubscription
						g.BalanceFallbackEnabled = true
						g.ImageCountBilling = true
						g.ImagePrice1K = otohaFloat(0.4)
					})
					const model = "gpt-image-2"
					wantTotal, wantActual := 0.8, 1.2 // two images at $0.40, rate 1.5
					if catalogOverride {
						entry := imageEntry(model, "1024x1024")
						entry.Price1K = otohaFloat(0.05)
						_, err := f.catalog.CreateImageEntry(context.Background(), f.group.ID, entry)
						require.NoError(t, err)
						wantTotal, wantActual = 0.1, 0.15 // catalog price beats group and token prices
					}
					resolver := newOpenAITokenImageChannelPricingResolverForTest(t, f.group.ID, model)
					billingRepo := &openAIRecordUsageBillingRepoStub{}
					users := &openAIRecordUsageUserRepoStub{}
					subs := &openAIRecordUsageSubRepoStub{}
					logs := &openAIRecordUsageLogRepoStub{inserted: true}
					key := &APIKey{ID: 10, GroupID: &f.group.ID, Group: f.group}
					user := &User{ID: 20, Balance: 10}
					account := &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
					var subscription *UserSubscription
					if planPays {
						subscription = &UserSubscription{ID: 40}
					}
					if gateway == "openai" {
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, users, subs, nil)
						svc.billingService, svc.resolver = resolver.billingService, resolver
						svc.SetOtohaImagePriceOverrides(f.catalog)
						require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
							Result: &OpenAIForwardResult{RequestID: "merge-images", Model: model,
								ImageCount: 2, ImageSize: "1K", ImageEndpoint: true,
								Usage: OpenAIUsage{InputTokens: 1_000_000, OutputTokens: 10_000}},
							APIKey: key, User: user, Account: account, Subscription: subscription,
						}))
					} else {
						svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billingRepo, users, subs)
						svc.billingService, svc.resolver = resolver.billingService, resolver
						svc.SetOtohaImagePriceOverrides(f.catalog)
						require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
							Result: &ForwardResult{RequestID: "merge-images", Model: model,
								ImageCount: 2, ImageSize: "1K", ImageEndpoint: true,
								Usage: ClaudeUsage{InputTokens: 1_000_000, OutputTokens: 10_000}},
							APIKey: key, User: user, Account: account, Subscription: subscription,
						}))
					}
					require.NotNil(t, logs.lastLog)
					require.Equal(t, 2, logs.lastLog.ImageCount)
					require.Equal(t, string(BillingModeImage), *logs.lastLog.BillingMode)
					require.InDelta(t, wantTotal, logs.lastLog.TotalCost, 1e-12)
					require.InDelta(t, wantActual, logs.lastLog.ActualCost, 1e-12)
					require.Equal(t, 1, billingRepo.calls)
					if planPays {
						require.Equal(t, int64(40), *billingRepo.lastCmd.SubscriptionID)
						require.InDelta(t, wantActual, billingRepo.lastCmd.SubscriptionCost, 1e-12)
						require.Zero(t, billingRepo.lastCmd.BalanceCost)
					} else {
						require.Nil(t, billingRepo.lastCmd.SubscriptionID)
						require.Zero(t, billingRepo.lastCmd.SubscriptionCost)
						require.InDelta(t, wantActual, billingRepo.lastCmd.BalanceCost, 1e-12)
					}
					require.Zero(t, users.deductCalls, "atomic billing must not also deduct through the legacy path")
					require.Zero(t, subs.incrementCalls)
				})
			}
		}
	}
}
