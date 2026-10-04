//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TASK-64: the app talks to each model in its provider's own format through the same key: Claude through Anthropic
// Messages (/v1/messages), DeepSeek through DeepSeek's Responses (/v1/responses, passed through), GPT and Grok through
// OpenAI Responses. The catalog names that format per model (`api`) and checks the model on the endpoint it names.

func otohaCatalogModelByID(t *testing.T, catalog *OtohaCatalog, id string) OtohaCatalogModel {
	t.Helper()
	for _, model := range catalog.Models {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("model %q is not in the catalog %v", id, otohaModelIDs(catalog))
	return OtohaCatalogModel{}
}

func TestOtohaCompositeCatalogNamesEachModelsNativeAPI(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.diagnoser.models[PlatformDeepseek] = []string{"deepseek-v4-flash"}
	f.diagnoser.models[PlatformGrok] = []string{"grok-4.7"}
	f.add(t, "gpt-6-luna", "claude-sonnet-4-5", "deepseek-v4-flash", "grok-4.7")

	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-luna", "claude-sonnet-4-5", "deepseek-v4-flash", "grok-4.7"}, otohaModelIDs(catalog))
	require.Equal(t, OtohaAPIOpenAIResponses, otohaCatalogModelByID(t, catalog, "gpt-6-luna").API)
	require.Equal(t, OtohaAPIAnthropicMessages, otohaCatalogModelByID(t, catalog, "claude-sonnet-4-5").API)
	require.Equal(t, OtohaAPIDeepSeekResponses, otohaCatalogModelByID(t, catalog, "deepseek-v4-flash").API)
	require.Equal(t, OtohaAPIOpenAIResponses, otohaCatalogModelByID(t, catalog, "grok-4.7").API)

	require.Equal(t, 0.3, otohaCatalogModelByID(t, catalog, "deepseek-v4-flash").Price.Input, "DeepSeek's price, times the rate 2")
	require.Equal(t, 6.0, otohaCatalogModelByID(t, catalog, "grok-4.7").Price.Input, "Grok's price, times the rate 2")

	body, err := json.Marshal(otohaCatalogModelByID(t, catalog, "claude-sonnet-4-5"))
	require.NoError(t, err)
	require.Contains(t, string(body), `"api":"anthropic-messages"`)

	_, byModel := f.view(t)
	require.Equal(t, OtohaAPIAnthropicMessages, byModel["claude-sonnet-4-5"].EffectiveAPI)
	require.Empty(t, byModel["claude-sonnet-4-5"].API, "derived, not set by the admin")
}

// A Claude model is called through /v1/messages, so a route that serves it only on /v1/responses does not make it
// callable, and a route that serves it only on /v1/messages does.
func TestOtohaCompositeClaudeIsCheckedOnMessages(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.routes = []CompositeModelRoute{
		{ID: 1, GroupID: otohaCompositeGroupID, PublicModel: "otoha-claude", MatchType: CompositeRouteMatchExact,
			TargetPlatform: PlatformAnthropic, UpstreamModel: "claude-sonnet-4-5", Endpoint: CompositeRouteEndpointMessages, Enabled: true},
		{ID: 2, GroupID: otohaCompositeGroupID, PublicModel: "responses-claude", MatchType: CompositeRouteMatchExact,
			TargetPlatform: PlatformAnthropic, UpstreamModel: "claude-sonnet-4-5", Endpoint: CompositeRouteEndpointResponses, Enabled: true},
	}
	f.add(t, "otoha-claude", "responses-claude")

	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.Equal(t, []string{"otoha-claude"}, otohaModelIDs(catalog))
	require.Equal(t, OtohaAPIAnthropicMessages, catalog.Models[0].API)
	require.Equal(t, 6.0, catalog.Models[0].Price.Input, "billed as the Claude model it is forwarded as")

	_, byModel := f.view(t)
	require.Equal(t, "claude-sonnet-4-5", byModel["otoha-claude"].RouteModel)
	require.Equal(t, OtohaCatalogProblemNoRoute, byModel["responses-claude"].Problem,
		"/v1/messages, which the app uses for Claude, cannot tell where to send it")
}

// Gemini has no format the app speaks natively; it stays out of the catalog unless the admin chooses one.
func TestOtohaCompositeGeminiHasNoNativeAPI(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.diagnoser.models[PlatformGemini] = []string{"gemini-2.5-pro"}
	f.add(t, "gemini-2.5-pro")

	_, byModel := f.view(t)
	gemini := byModel["gemini-2.5-pro"]
	require.Equal(t, OtohaCatalogProblemNoNativeAPI, gemini.Problem)
	require.Equal(t, PlatformGemini, gemini.RoutePlatform)
	require.Empty(t, gemini.EffectiveAPI)
}

// The admin can choose the format: OpenAI Responses for a Claude model (the gateway converts), Anthropic Messages for
// a Gemini or GPT model (the gateway converts Messages); a format that does not reach the provider, or that the
// provider does not answer in, hides the model.
func TestOtohaCompositeAdminChosenAPI(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	f.diagnoser.models[PlatformGemini] = []string{"gemini-2.5-pro"}
	f.diagnoser.models[PlatformDeepseek] = []string{"deepseek-v4-flash"}
	f.add(t, "claude-sonnet-4-5", "gemini-2.5-pro", "gpt-6-luna", "deepseek-v4-flash")
	entries, err := f.repo.ListByGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	set := func(model, api string) {
		for _, e := range entries {
			if e.ModelID == model {
				_, err := f.svc.UpdateEntry(context.Background(), otohaCompositeGroupID, e.ID, OtohaCatalogEntryInput{ModelID: model, Enabled: true, API: api})
				require.NoError(t, err)
			}
		}
	}
	before, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)

	set("claude-sonnet-4-5", OtohaAPIOpenAIResponses)
	set("gemini-2.5-pro", OtohaAPIAnthropicMessages)
	set("gpt-6-luna", "  Anthropic-Messages ")

	catalog, err := f.svc.CatalogForGroup(context.Background(), otohaCompositeGroupID)
	require.NoError(t, err)
	require.NotEqual(t, before.Revision, catalog.Revision, "a changed format changes the revision")
	require.Equal(t, []string{"claude-sonnet-4-5", "gemini-2.5-pro", "gpt-6-luna", "deepseek-v4-flash"}, otohaModelIDs(catalog))
	require.Equal(t, OtohaAPIOpenAIResponses, otohaCatalogModelByID(t, catalog, "claude-sonnet-4-5").API)
	require.Equal(t, OtohaAPIAnthropicMessages, otohaCatalogModelByID(t, catalog, "gemini-2.5-pro").API)
	require.Equal(t, OtohaAPIAnthropicMessages, otohaCatalogModelByID(t, catalog, "gpt-6-luna").API, "the admin's choice, normalized")
	require.Equal(t, OtohaAPIDeepSeekResponses, otohaCatalogModelByID(t, catalog, "deepseek-v4-flash").API)

	set("gemini-2.5-pro", OtohaAPIOpenAIResponses)
	set("gpt-6-luna", OtohaAPIDeepSeekResponses)
	set("deepseek-v4-flash", OtohaAPIOpenAIResponses)
	_, byModel := f.view(t)
	require.Equal(t, OtohaCatalogProblemAPIUnreachable, byModel["gemini-2.5-pro"].Problem,
		"/v1/responses would send a Gemini account an Anthropic request")
	require.Equal(t, OtohaAPIOpenAIResponses, byModel["gemini-2.5-pro"].EffectiveAPI, "the admin's choice shows on the hidden row")
	require.Equal(t, OtohaCatalogProblemAPIUnreachable, byModel["gpt-6-luna"].Problem,
		"only DeepSeek answers in DeepSeek's dialect")
	require.Equal(t, OtohaCatalogProblemAPIUnreachable, byModel["deepseek-v4-flash"].Problem,
		"DeepSeek answers /v1/responses in its own dialect, which OpenAI parsers reject")
}

func TestOtohaCatalogRejectsAnUnknownAPI(t *testing.T) {
	f := newOtohaCatalogFixture()
	_, err := f.svc.CreateEntry(context.Background(), otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true, API: "gemini"})
	require.Error(t, err)
	entry, err := f.svc.CreateEntry(context.Background(), otohaGroupID, OtohaCatalogEntryInput{ModelID: "gpt-6-luna", Enabled: true, API: " "})
	require.NoError(t, err)
	require.Empty(t, entry.API, "blank derives the format from the provider")
}

// A group of one provider names that provider's format.
func TestOtohaSingleProviderGroupNamesItsFormat(t *testing.T) {
	gateway := &fakeOtohaDiagnoser{supported: true}
	routing := otohaDiagnoserRouting{gateway: gateway, openai: gateway}
	ctx := context.Background()
	for platform, want := range map[string]string{
		PlatformAnthropic:   OtohaAPIAnthropicMessages,
		PlatformAntigravity: OtohaAPIAnthropicMessages,
		PlatformDeepseek:    OtohaAPIDeepSeekResponses,
		PlatformOpenAI:      OtohaAPIOpenAIResponses,
		PlatformGrok:        OtohaAPIOpenAIResponses,
		PlatformKimi:        OtohaAPIOpenAIResponses,
	} {
		route := routing.Route(ctx, &Group{ID: 1, Platform: platform}, "m", "")
		require.Empty(t, route.Problem, platform)
		require.Equal(t, want, route.API, platform)
	}
	route := routing.Route(ctx, &Group{ID: 1, Platform: PlatformGemini}, "gemini-2.5-pro", "")
	require.Equal(t, OtohaCatalogProblemNoNativeAPI, route.Problem)
	route = routing.Route(ctx, &Group{ID: 1, Platform: PlatformGemini}, "gemini-2.5-pro", OtohaAPIAnthropicMessages)
	require.Empty(t, route.Problem, "/v1/messages reaches Gemini accounts")
	require.Equal(t, OtohaAPIAnthropicMessages, route.API)
	route = routing.Route(ctx, &Group{ID: 1, Platform: PlatformTypeSafe}, "jev-latest", OtohaAPIAnthropicMessages)
	require.Equal(t, OtohaCatalogProblemAPIUnreachable, route.Problem, "TypeSafe takes only its own protocol")
}

func TestOtohaCompositePrefillNamesTheFormat(t *testing.T) {
	f := newOtohaCompositeFixture(t)
	draft, err := f.svc.Prefill(context.Background(), otohaCompositeGroupID, "claude-sonnet-4-5")
	require.NoError(t, err)
	require.Equal(t, OtohaAPIAnthropicMessages, draft.RouteAPI)
	require.Empty(t, draft.Entry.API)
}
