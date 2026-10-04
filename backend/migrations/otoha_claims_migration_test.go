package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtohaClaimsMigration(t *testing.T) {
	content, err := FS.ReadFile("242_otoha_claims.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS otoha_claims")
	require.Contains(t, sql, "code_hash VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "used_at TIMESTAMPTZ NULL")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS otoha_claims_code_hash_key ON otoha_claims (code_hash)")
	require.NotContains(t, strings.ToLower(sql), "concurrently", "runs inside the migration transaction")
}
