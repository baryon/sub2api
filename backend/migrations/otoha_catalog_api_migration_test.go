package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtohaCatalogAPIMigration(t *testing.T) {
	content, err := FS.ReadFile("245_otoha_catalog_api.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE otoha_catalog_models")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS api VARCHAR(32) NOT NULL DEFAULT ''",
		"existing entries derive the format from the provider")
}
