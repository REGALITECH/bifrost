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
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{}}`}).Error)
	catalog := modelcatalog.NewTestCatalogWithDatasheet(datasheet.New(store, logger, datasheet.Config{}), store)
	resolver := NewBudgetResolver(nil, catalog, logger, &mockInMemoryStore{configuredProviders: map[schemas.ModelProvider]configstore.ProviderConfig{configstore.TranscriptionUsageProvider: {CustomProviderConfig: &schemas.CustomProviderConfig{BaseProviderType: schemas.OpenAI, IsKeyLess: true, AllowedRequests: &schemas.AllowedRequests{}}}}})
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		vk := buildVirtualKey(tenant, "sk-bf-"+tenant, tenant, true)
		vk.ProviderConfigs = []tables.TableVirtualKeyProviderConfig{buildProviderConfig("transcription", []string{"*"}), buildProviderConfig("vllm", []string{"llama"})}
		for _, model := range []string{"brand-new-1", "brand-new-2"} {
			if tenant == "tenant-a" {
				require.False(t, resolver.isModelAllowed(vk, configstore.TranscriptionUsageProvider, model))
				require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, model))
			}
			require.True(t, resolver.isModelAllowed(vk, configstore.TranscriptionUsageProvider, model))
		}
		require.False(t, resolver.isModelAllowed(vk, configstore.TranscriptionUsageProvider, "unregistered"))
		require.True(t, resolver.isModelAllowed(vk, schemas.VLLM, "llama"))
		require.False(t, resolver.isModelAllowed(vk, schemas.VLLM, "brand-new-1"))
		vk.ProviderConfigs[0].BlacklistedModels = schemas.BlackList{"brand-new-1"}
		require.False(t, resolver.isModelAllowed(vk, configstore.TranscriptionUsageProvider, "brand-new-1"))
		vk.ProviderConfigs[0].BlacklistedModels = nil
		vk.ProviderConfigs[0].AllowedModels = schemas.WhiteList{"brand-new-1"}
		require.False(t, resolver.isModelAllowed(vk, configstore.TranscriptionUsageProvider, "brand-new-2"))
		vk.ProviderConfigs[0].AllowedModels = nil
		require.False(t, resolver.isModelAllowed(vk, configstore.TranscriptionUsageProvider, "brand-new-1"))
	}
	usage := &schemas.BifrostLLMUsage{PromptTokens: 2500, TotalTokens: 2500}
	require.Zero(t, catalog.CalculateCostForUsage(usage, configstore.TranscriptionUsageProvider, "brand-new-2", schemas.TranscriptionRequest, nil))

	providerID := "transcription"
	require.NoError(t, catalog.SetPricingOverrides([]tables.TablePricingOverride{{ID: "stt-price", ScopeKind: "provider", ProviderID: &providerID, MatchType: "exact", Pattern: "brand-new-2", RequestTypes: []schemas.RequestType{schemas.TranscriptionRequest}, PricingPatchJSON: `{"input_cost_per_token":0.003}`}}))
	require.InDelta(t, 7.5, catalog.CalculateCostForUsage(usage, configstore.TranscriptionUsageProvider, "brand-new-2", schemas.TranscriptionRequest, nil), 1e-9)
	require.NoError(t, configstore.EnsureTranscriptionModel(ctx, store, "brand-new-2"))
	require.InDelta(t, 7.5, catalog.CalculateCostForUsage(usage, configstore.TranscriptionUsageProvider, "brand-new-2", schemas.TranscriptionRequest, nil), 1e-9)
	require.True(t, catalog.IsModelAllowedForProvider(configstore.TranscriptionUsageProvider, "brand-new-2", nil, schemas.WhiteList{"*"}))
}
