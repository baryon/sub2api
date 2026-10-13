//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TASK-82: the admin plan list carries every language of the plan so the editor can fill each; a plan without
// any sends an empty object, never null.
func TestAdminSubscriptionPlansForResponseIncludesTranslations(t *testing.T) {
	plans := []*dbent.SubscriptionPlan{
		{ID: 1, GroupID: 7, Name: "标准版", Translations: map[string]map[string]string{"en": {"name": "Standard"}}},
		{ID: 2, GroupID: 7, Name: "旧版"},
	}
	got := adminSubscriptionPlansForResponse(plans, map[int64]service.PlanGroupInfo{})

	body, err := json.Marshal(got[0])
	require.NoError(t, err)
	require.Contains(t, string(body), `"translations":{"en":{"name":"Standard"}}`)
	require.Equal(t, "标准版", got[0].Name, "the plan's own text is not translated for the admin")

	body, err = json.Marshal(got[1])
	require.NoError(t, err)
	require.Contains(t, string(body), `"translations":{}`)
}
