package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtohaPlanTranslationsMigration(t *testing.T) {
	content, err := FS.ReadFile("251_otoha_plan_translations.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE subscription_plans")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS translations JSONB NOT NULL DEFAULT '{}'::jsonb",
		"existing plans get no translations and keep showing their own text")
	require.NotContains(t, sql, "UPDATE", "no plan copy in migrations: the admin writes the languages")
	require.NotContains(t, sql, "INSERT")
}
