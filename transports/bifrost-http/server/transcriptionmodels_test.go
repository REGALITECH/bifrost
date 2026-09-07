package server

import (
	"context"
	"path/filepath"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
)

func TestTranscriptionCatalogRegistrationPreservesState(t *testing.T) {
	ctx := context.Background()
	logger := bifrost.NewNoOpLogger()
	store, err := configstore.NewConfigStore(ctx, &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "config.db")}}, logger)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(ctx) })
	require.False(t, store.DB().Migrator().HasColumn(&tables.TableModel{}, "enabled"))
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{}}`}).Error)
	catalog := modelcatalog.NewTestCatalogWithDatasheet(datasheet.New(store, logger, datasheet.Config{}), store)
	lib.SetLogger(logger)
	s := &BifrostHTTPServer{Config: &lib.Config{ConfigStore: store, ModelCatalog: catalog}}

	ensure := []handlers.ModelPricingAttributesEntry{{Provider: "transcription", Model: "asr", CreateIfMissing: true}}
	require.NoError(t, s.UpsertModelPricingAttributes(ctx, ensure))
	require.Equal(t, []string{"asr"}, catalog.GetModelsForProvider(configstore.TranscriptionUsageProvider))
	require.NoError(t, store.DB().Model(&tables.TableModelPricing{}).Where("provider = ? AND model = ?", "transcription", "asr").Updates(map[string]any{"input_cost_per_token": 0.002, "additional_attributes": "{\"owner\":\"existing\"}"}).Error)
	require.NoError(t, s.UpsertModelPricingAttributes(ctx, ensure))
	state, err := configstore.GetTranscriptionModel(ctx, store, "asr")
	require.NoError(t, err)
	require.Equal(t, 0.002, *state.InputCostPerToken)
	price, err := configstore.GetTranscriptionModelPrice(ctx, store, "asr")
	require.NoError(t, err)
	require.JSONEq(t, `{"owner":"existing"}`, price.AdditionalAttributesJSON)
	require.Equal(t, []string{"asr"}, catalog.GetModelsForProvider(configstore.TranscriptionUsageProvider))
	require.Equal(t, []string{"asr"}, catalog.GetUnfilteredModelsForProvider(configstore.TranscriptionUsageProvider))
	// A failing later entry rolls back both registration and price inserts.
	require.Error(t, s.UpsertModelPricingAttributes(ctx, []handlers.ModelPricingAttributesEntry{{Provider: "transcription", Model: "rollback", CreateIfMissing: true}, {Provider: "openai", Model: "nonexistent"}}))
	_, err = configstore.GetTranscriptionModel(ctx, store, "rollback")
	require.Error(t, err)
}
