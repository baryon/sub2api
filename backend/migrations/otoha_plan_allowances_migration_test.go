package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtohaPlanAllowancesMigration(t *testing.T) {
	content, err := FS.ReadFile("244_otoha_plan_allowances.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, column := range []string{"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd"} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+column+" DECIMAL(20,8)")
	}
	require.Contains(t, sql, "ALTER TABLE subscription_plans")
	require.Contains(t, sql, "ALTER TABLE user_subscriptions")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS plan_id BIGINT")
	require.NotContains(t, sql, "NOT NULL", "existing plans and subscriptions keep no allowance of their own")
	require.NotContains(t, sql, "REFERENCES", "plans are hard-deleted; the subscription keeps the id")
}
