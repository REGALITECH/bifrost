package configstore

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/migrator"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProviderWithoutDedicatedPricingPreservesLegacyColumn(t *testing.T) {
	store, db := setupFullMigrationDB(t)
	ctx := context.Background()
	require.False(t, db.Migrator().HasColumn(&tables.TableProvider{}, "typesafe_config_json"))
	require.NoError(t, store.AddProvider(ctx, schemas.Typesafe, ProviderConfig{
		NetworkConfig: &schemas.NetworkConfig{BaseURL: "https://api.typesafe.ai"},
	}))
	// Simulate a database initialized by the experimental credit-pricing branch.
	// Removing the setting must not require dropping that column or its data.
	legacyMigration := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
		ID: "add_typesafe_config_json_column",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec("ALTER TABLE config_providers ADD COLUMN typesafe_config_json TEXT").Error
		},
	}})
	require.NoError(t, legacyMigration.Migrate())
	legacy := `{"usd_per_credit":"0.002"}`
	require.NoError(t, db.Exec("UPDATE config_providers SET typesafe_config_json = ? WHERE name = ?", legacy, "typesafe").Error)
	require.NoError(t, db.AutoMigrate(&tables.TableProvider{}))
	require.NoError(t, triggerMigrations(ctx, db, testMigrationLogger))
	require.False(t, areThereAnyPendingMigrations(ctx, db, testMigrationLogger))
	config, err := store.GetProviderConfig(ctx, schemas.Typesafe)
	require.NoError(t, err)
	require.Equal(t, "https://api.typesafe.ai", config.NetworkConfig.BaseURL)
	config.NetworkConfig.DefaultRequestTimeoutInSeconds = 20
	require.NoError(t, store.UpdateProvider(ctx, schemas.Typesafe, *config))
	var retained string
	require.NoError(t, db.Raw("SELECT typesafe_config_json FROM config_providers WHERE name = ?", "typesafe").Scan(&retained).Error)
	require.Equal(t, legacy, retained)
	updated, err := store.GetProviderConfig(ctx, schemas.Typesafe)
	require.NoError(t, err)
	require.Equal(t, 20, updated.NetworkConfig.DefaultRequestTimeoutInSeconds)
}
