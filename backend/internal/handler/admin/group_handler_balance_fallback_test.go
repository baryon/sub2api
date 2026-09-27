package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRequestsDecodeBalanceFallback(t *testing.T) {
	var createReq CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"otoha","subscription_type":"subscription","balance_fallback_enabled":true}`), &createReq))
	require.True(t, createReq.BalanceFallbackEnabled)

	var updateReq UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"balance_fallback_enabled":false}`), &updateReq))
	require.NotNil(t, updateReq.BalanceFallbackEnabled)
	require.False(t, *updateReq.BalanceFallbackEnabled)

	var omitted UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.Nil(t, omitted.BalanceFallbackEnabled)
}
