//go:build unit

package handler

import (
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-82: the buy page and the plan list show a plan's text in the request's language, falling back to the
// plan's own text; the languages themselves are not sent to buyers.
func translatedPlanForBuyers() *dbent.SubscriptionPlan {
	return &dbent.SubscriptionPlan{
		ID: 1, GroupID: 7, Name: "标准版", Description: "适合日常使用", Features: "每月额度\n全部模型", Price: 20,
		Translations: map[string]map[string]string{"en": {"name": "Standard", "description": "For everyday use", "features": "Monthly allowance\nAll models"}},
	}
}

func TestCheckoutPlanTextFollowsTheRequestsLanguage(t *testing.T) {
	gi := service.PlanGroupInfo{Name: "Otoha"}

	en := checkoutPlanFromPlan(translatedPlanForBuyers(), gi, "en-US,en;q=0.9")
	require.Equal(t, "Standard", en.Name)
	require.Equal(t, "For everyday use", en.Description)
	require.Equal(t, []string{"Monthly allowance", "All models"}, en.Features)

	zh := checkoutPlanFromPlan(translatedPlanForBuyers(), gi, "zh-CN,zh;q=0.9")
	require.Equal(t, "标准版", zh.Name)
	require.Equal(t, []string{"每月额度", "全部模型"}, zh.Features)

	none := checkoutPlanFromPlan(translatedPlanForBuyers(), gi, "")
	require.Equal(t, "标准版", none.Name)

	body, err := json.Marshal(en)
	require.NoError(t, err)
	require.NotContains(t, string(body), "translations", "buyers get the text of their language only")
}

func TestPlanListTextFollowsTheRequestsLanguage(t *testing.T) {
	gi := service.PlanGroupInfo{Name: "Otoha"}
	en := planListItemFromPlan(translatedPlanForBuyers(), gi, "en")
	require.Equal(t, "Standard", en.Name)
	require.Equal(t, "For everyday use", en.Description)
	require.Equal(t, "Monthly allowance\nAll models", en.Features, "this list keeps features as one text")
	require.Equal(t, "标准版", planListItemFromPlan(translatedPlanForBuyers(), gi, "fr").Name)
}
