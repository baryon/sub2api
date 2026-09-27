package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupBalanceFallbackMigration(t *testing.T) {
	content, err := FS.ReadFile("241_group_balance_fallback.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql,
		"ADD COLUMN IF NOT EXISTS balance_fallback_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.balance_fallback_enabled")
}
