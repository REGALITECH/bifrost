package lib

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
)

func TestMetronomeManagementRestartPreservesDBSettings(t *testing.T) {
	initTestLogger()
	ctx := context.Background()
	dir := t.TempDir()
	store := createTestSQLiteConfigStore(t, dir)
	saved := &tables.TablePlugin{Name: "metronome", Enabled: false, Version: 2, Config: map[string]any{
		"api_key": "env.METRONOME_MANAGEMENT_TEST_KEY", "dry_run": false,
		"model_mapping": map[string]any{"openai/test": "billing-model"},
	}}
	t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "test-runtime-only-key")
	require.NoError(t, store.CreatePlugin(ctx, saved))
	custom := &tables.TablePlugin{Name: "existing-custom", Path: schemas.Ptr("/plugins/existing-custom.so"), IsCustom: true, Enabled: false, Config: map[string]any{"preserved": true}}
	require.NoError(t, store.CreatePlugin(ctx, custom))
	// Close and reopen the actual store, not just a mock's in-memory data.
	require.NoError(t, store.Close(ctx))
	store = createTestSQLiteConfigStore(t, dir)
	// Other plugins still come from the chart's config file. Metronome is absent.
	file := &ConfigData{Plugins: []*schemas.PluginConfig{{Name: "telemetry", Enabled: true, Config: map[string]any{}}}}
	for range 2 {
		config := &Config{ConfigStore: store}
		loadPlugins(ctx, config, file)
		stored, err := store.GetPlugin(ctx, "metronome")
		require.NoError(t, err)
		require.Equal(t, saved.Config, stored.Config)
		require.Equal(t, saved.Enabled, stored.Enabled)
		require.Equal(t, int16(2), stored.Version)
		require.False(t, stored.IsCustom)
		require.NotContains(t, stored.ConfigJSON, "test-runtime-only-key")
		_, err = store.GetPlugin(ctx, "telemetry")
		require.NoError(t, err)
		other, err := store.GetPlugin(ctx, custom.Name)
		require.NoError(t, err)
		require.Equal(t, custom.Config, other.Config)
		require.True(t, other.IsCustom)
	}
}

func TestMetronomeManagementStartupNeverPersistsResolvedKey(t *testing.T) {
	initTestLogger()
	ctx := context.Background()
	store := createTestSQLiteConfigStore(t, t.TempDir())
	empty, err := pluginConfigForStorage(&schemas.PluginConfig{Name: "metronome"})
	require.NoError(t, err, "omitting config must still allow builtin defaults")
	require.Equal(t, map[string]any{}, empty)
	t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "test-runtime-only-key")
	raw := map[string]any{
		"api_key":          map[string]any{"ref": "env.METRONOME_MANAGEMENT_TEST_KEY", "type": "env", "value": "test-runtime-only-key"},
		"customer_mapping": map[string]any{}, "default_customer_id": "",
	}
	loadPlugins(ctx, &Config{ConfigStore: store}, &ConfigData{Plugins: []*schemas.PluginConfig{{Name: "metronome", Config: raw}}})
	stored, err := store.GetPlugin(ctx, "metronome")
	require.NoError(t, err)
	require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", stored.Config.(map[string]any)["api_key"])
	require.NotContains(t, stored.ConfigJSON, "test-runtime-only-key")
	require.NotContains(t, stored.Config, "customer_mapping")
	// A literal in a later startup file is rejected before the DB write.
	loadPlugins(ctx, &Config{ConfigStore: store}, &ConfigData{Plugins: []*schemas.PluginConfig{{Name: "metronome", Version: schemas.Ptr(int16(3)), Config: map[string]any{"api_key": "test-literal-key"}}}})
	stored, err = store.GetPlugin(ctx, "metronome")
	require.NoError(t, err)
	require.NotContains(t, stored.ConfigJSON, "test-literal-key")
	require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", stored.Config.(map[string]any)["api_key"])
}
