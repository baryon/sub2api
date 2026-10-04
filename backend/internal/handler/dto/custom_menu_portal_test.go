package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// show_in_portal marks a custom menu item that stays visible in the Otoha portal (TASK-61). It has to survive
// parsing, or the public settings API and the admin save path would drop it.
func TestCustomMenuItemsKeepTheirPortalMark(t *testing.T) {
	raw := `[{"id":"help","label":"Help","url":"https://example.com/help","visibility":"user","sort_order":1,"show_in_portal":true},` +
		`{"id":"docs","label":"Docs","url":"https://example.com/docs","visibility":"user","sort_order":2}]`

	items := ParseUserVisibleMenuItems(raw)
	require.Len(t, items, 2)
	require.True(t, items[0].ShowInPortal)
	require.False(t, items[1].ShowInPortal)

	out, err := json.Marshal(items)
	require.NoError(t, err)
	require.Contains(t, string(out), `"show_in_portal":true`)
	require.Equal(t, 1, strings.Count(string(out), "show_in_portal"), "unmarked items leave the field out")
}
