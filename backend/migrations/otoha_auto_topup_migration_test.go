package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtohaAutoTopUpMigration(t *testing.T) {
	content, err := FS.ReadFile("250_otoha_auto_topup.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS otoha_auto_topups")
	require.Contains(t, sql, "enabled BOOLEAN NOT NULL DEFAULT FALSE", "auto top-up is off until the user turns it on")
	require.Contains(t, sql, "threshold_usd NUMERIC(20,2) NOT NULL DEFAULT 5")
	require.Contains(t, sql, "amount_usd NUMERIC(20,2) NOT NULL DEFAULT 20")
	require.Contains(t, sql, "monthly_cap_usd NUMERIC(20,2) NOT NULL DEFAULT 100")
	require.NotContains(t, sql, "card_number", "only brand and last four digits are kept")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS otoha_auto_topup_charges")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS otoha_auto_topup_charges_one_pending ON otoha_auto_topup_charges (user_id) WHERE status = 'pending'",
		"one pending charge per user is enforced by the database")
}
