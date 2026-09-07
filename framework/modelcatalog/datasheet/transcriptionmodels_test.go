package datasheet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
)

func TestTranscriptionPricingIgnoresExternalDatasheet(t *testing.T) {
	ctx := context.Background()
	logger := bifrost.NewNoOpLogger()
	dir := t.TempDir()
	store, err := configstore.NewConfigStore(ctx, &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: filepath.Join(dir, "config.db")}}, logger)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(ctx) })
	require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, "asr"))
	require.NoError(t, store.DB().Model(&tables.TableModelPricing{}).Where("provider = ? AND model = ?", "transcription", "asr").Update("input_cost_per_token", 0.002).Error)
	path := filepath.Join(dir, "pricing.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"asr":{"provider":"transcription","mode":"audio_transcription","input_cost_per_token":0,"output_cost_per_token":0},"unregistered":{"provider":"transcription","mode":"audio_transcription","input_cost_per_token":0},"llama":{"provider":"vllm","mode":"chat","input_cost_per_token":0.01}}`), 0600))
	s := New(store, logger, Config{URL: "file://" + path})
	require.NoError(t, s.SyncFromURL(ctx))
	state, err := configstore.GetTranscriptionModel(ctx, store, "asr")
	require.NoError(t, err)
	require.Equal(t, 0.002, *state.InputCostPerToken)
	_, err = configstore.GetTranscriptionModelPrice(ctx, store, "unregistered")
	require.Error(t, err)
	var price tables.TableModelPricing
	require.NoError(t, store.DB().Where("provider = ? AND model = ?", "vllm", "llama").First(&price).Error)
	require.Equal(t, 0.01, *price.InputCostPerToken)
}
