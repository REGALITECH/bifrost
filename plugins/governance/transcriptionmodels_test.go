package governance

import (
	"context"
	"path/filepath"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
	"github.com/stretchr/testify/require"
)

func TestTranscriptionWildcardAndPricing(t *testing.T) {
	ctx := context.Background()
	logger := bifrost.NewNoOpLogger()
	store, err := configstore.NewConfigStore(ctx, &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "config.db")}}, logger)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(ctx) })
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription"}).Error)
	catalog := modelcatalog.NewTestCatalogWithDatasheet(datasheet.New(store, logger, datasheet.Config{}), store)
	resolver := NewBudgetResolver(nil, catalog, logger, nil)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		vk := buildVirtualKey(tenant, "sk-bf-"+tenant, tenant, true)
		vk.ProviderConfigs = []tables.TableVirtualKeyProviderConfig{buildProviderConfig("transcription", []string{"*"}), buildProviderConfig("vllm", []string{"llama"})}
		for _, model := range []string{"brand-new-1", "brand-new-2"} {
			if tenant == "tenant-a" {
				require.False(t, resolver.isModelAllowed(vk, schemas.Transcription, model))
				require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, model))
			}
			require.True(t, resolver.isModelAllowed(vk, schemas.Transcription, model))
		}
		require.False(t, resolver.isModelAllowed(vk, schemas.Transcription, "unregistered"))
		require.True(t, resolver.isModelAllowed(vk, schemas.VLLM, "llama"))
		require.False(t, resolver.isModelAllowed(vk, schemas.VLLM, "brand-new-1"))
		vk.ProviderConfigs[0].BlacklistedModels = schemas.BlackList{"brand-new-1"}
		require.False(t, resolver.isModelAllowed(vk, schemas.Transcription, "brand-new-1"))
		vk.ProviderConfigs[0].BlacklistedModels = nil
		vk.ProviderConfigs[0].AllowedModels = schemas.WhiteList{"brand-new-1"}
		require.False(t, resolver.isModelAllowed(vk, schemas.Transcription, "brand-new-2"))
		vk.ProviderConfigs[0].AllowedModels = nil
		require.False(t, resolver.isModelAllowed(vk, schemas.Transcription, "brand-new-1"))
	}
	require.NoError(t, configstore.SetTranscriptionModelEnabled(ctx, store, "brand-new-1", false))
	require.False(t, catalog.IsModelAllowedForProvider(schemas.Transcription, "brand-new-1", nil, schemas.WhiteList{"*"}))
	usage := &schemas.BifrostLLMUsage{PromptTokens: 2500, TotalTokens: 2500}
	require.Zero(t, catalog.CalculateCostForUsage(usage, schemas.Transcription, "brand-new-2", schemas.TranscriptionRequest, nil))
	require.NoError(t, store.DB().Model(&tables.TableModelPricing{}).Where("provider = ? AND model = ?", "transcription", "brand-new-2").Update("input_cost_per_token", 0.002).Error)
	// No catalog refresh: another replica sees the current base price immediately.
	require.InDelta(t, 5.0, catalog.CalculateCostForUsage(usage, schemas.Transcription, "brand-new-2", schemas.TranscriptionRequest, nil), 1e-9)
	require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, "brand-new-2"))
	require.InDelta(t, 5.0, catalog.CalculateCostForUsage(usage, schemas.Transcription, "brand-new-2", schemas.TranscriptionRequest, nil), 1e-9)

	providerID := "transcription"
	require.NoError(t, catalog.SetPricingOverrides([]tables.TablePricingOverride{{ID: "stt-price", ScopeKind: "provider", ProviderID: &providerID, MatchType: "exact", Pattern: "brand-new-2", RequestTypes: []schemas.RequestType{schemas.TranscriptionRequest}, PricingPatchJSON: `{"input_cost_per_token":0.003}`}}))
	require.InDelta(t, 7.5, catalog.CalculateCostForUsage(usage, schemas.Transcription, "brand-new-2", schemas.TranscriptionRequest, nil), 1e-9)
	require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, "brand-new-2"))
	require.InDelta(t, 7.5, catalog.CalculateCostForUsage(usage, schemas.Transcription, "brand-new-2", schemas.TranscriptionRequest, nil), 1e-9)
	require.NoError(t, store.DB().Model(&tables.TableModelPricing{}).Where("provider = ? AND model = ?", "transcription", "brand-new-2").Update("input_cost_per_token", nil).Error)
	require.False(t, catalog.IsModelAllowedForProvider(schemas.Transcription, "brand-new-2", nil, schemas.WhiteList{"*"}))
}
