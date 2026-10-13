package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtohaCatalogImageModelsMigration(t *testing.T) {
	content, err := FS.ReadFile("248_otoha_catalog_image_models.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS otoha_catalog_image_models")
	require.Contains(t, sql, "price_1k DOUBLE PRECISION,", "a price left empty follows billing")
	require.Contains(t, sql, "REFERENCES groups(id) ON DELETE CASCADE")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS otohacatalogimagemodel_group_id_model_id")
}

func TestOtohaCatalogImageModelsDiscoveryMigration(t *testing.T) {
	content, err := FS.ReadFile("249_otoha_catalog_image_models_discovery.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE otoha_catalog_image_models")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'manual'", "entries made before are the admin's")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS manual_fields JSONB")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS unavailable_since TIMESTAMPTZ")
}
