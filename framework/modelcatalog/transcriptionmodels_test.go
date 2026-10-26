package modelcatalog

import (
	"context"
	"path/filepath"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
	"github.com/stretchr/testify/require"
)

// TestTranscriptionModelsBypassProviderMemo pins that a transcription usage
// model registered at runtime is listed at once. Registration writes only the
// config store, which does not bump the generation that stamps the
// GetModelsForProvider memo, so a memoized answer would hide the new model
// until the next datasheet, live or keyconfig write.
func TestTranscriptionModelsBypassProviderMemo(t *testing.T) {
	ctx := context.Background()
	logger := bifrost.NewNoOpLogger()
	store, err := configstore.NewConfigStore(ctx, &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "config.db")}}, logger)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(ctx) })
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{}}`}).Error)
	mc := NewTestCatalogWithDatasheet(datasheet.New(store, logger, datasheet.Config{}), store)
	provider := configstore.TranscriptionUsageProvider

	require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, "asr-a"))
	require.Equal(t, []string{"asr-a"}, mc.GetModelsForProvider(provider))

	generation := mc.catalogGeneration()
	require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, "asr-b"))
	require.Equal(t, generation, mc.catalogGeneration(), "registration must not depend on a catalog store write")
	require.Equal(t, []string{"asr-a", "asr-b"}, mc.GetModelsForProvider(provider))
	require.Equal(t, []string{"asr-a", "asr-b"}, mc.GetUnfilteredModelsForProvider(provider))
	require.True(t, mc.IsModelAllowedForProvider(provider, "asr-b", nil, schemas.WhiteList{"*"}))
	require.False(t, mc.IsModelAllowedForProvider(provider, "asr-c", nil, schemas.WhiteList{"*"}))
}
