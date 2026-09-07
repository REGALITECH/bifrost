package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/stretchr/testify/require"
)

type transcriptionVKManager struct {
	pricingOverrideTestGovernanceManager
	store configstore.ConfigStore
}

func (m transcriptionVKManager) ReloadVirtualKey(ctx context.Context, id string) (*tables.TableVirtualKey, error) {
	return m.store.GetVirtualKey(ctx, id)
}

func TestTranscriptionVirtualKeyWithoutExternalKeys(t *testing.T) {
	SetLogger(testLogger{})
	store := newTestConfigStore(t)
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{}}`}).Error)
	h := &GovernanceHandler{configStore: store, governanceManager: transcriptionVKManager{store: store}}
	for _, body := range []string{
		`{"name":"tenant-one","provider_configs":[{"provider":"transcription","allowed_models":["*"]}]}`,
		`{"name":"tenant-two","provider_configs":[{"provider":"transcription","allowed_models":["*"],"key_ids":[]}]}`,
	} {
		ctx := newTestRequestCtx(body)
		h.createVirtualKey(ctx)
		require.Equal(t, 200, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	keys, err := store.GetVirtualKeys(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 2)
	for _, key := range keys {
		require.Len(t, key.ProviderConfigs, 1)
		pc := key.ProviderConfigs[0]
		require.Equal(t, "transcription", pc.Provider)
		require.Equal(t, schemas.WhiteList{"*"}, pc.AllowedModels)
		require.Empty(t, pc.Keys)
	}
	ctx := newTestRequestCtx(`{"name":"invalid-provider","provider_configs":[{"provider":"not-configured","allowed_models":["*"]}]}`)
	h.createVirtualKey(ctx)
	require.Equal(t, 400, ctx.Response.StatusCode())
}

func TestTranscriptionUsageWithRealGovernance(t *testing.T) {
	SetLogger(testLogger{})
	events := []string{}
	h, loggingPlugin, _ := newTranscriptionUsageTestHandler(t, &events)
	store := h.config.ConfigStore
	manager := &GovernanceHandler{configStore: store, governanceManager: transcriptionVKManager{store: store}}
	for _, name := range []string{"a", "b"} {
		ctx := newTestRequestCtx(fmt.Sprintf(`{"name":%q,"provider_configs":[{"provider":"transcription","allowed_models":["*"]}]}`, name))
		manager.createVirtualKey(ctx)
		require.Equal(t, 200, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	plugin, err := governance.Init(context.Background(), nil, testLogger{}, store, nil, h.config.ModelCatalog, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, plugin.Cleanup()) })
	plugins := []schemas.BasePlugin{loggingPlugin, plugin}
	h.config.BasePlugins.Store(&plugins)
	keys, err := store.GetVirtualKeys(context.Background())
	require.NoError(t, err)
	// Register after plugin startup and after VK creation: no provider reload required.
	require.NoError(t, configstore.EnsureTranscriptionModel(context.Background(), store, "later-asr"))
	for i, key := range keys {
		body := `{"audio_ms":2500,"turns":1,"outcome":"completed","session_id":"s","seq":1,"model":"transcription/later-asr"}`
		ctx := newTranscriptionUsageTestContext(body, key.Value.GetValue(), fmt.Sprintf("usage-%d", i))
		h.recordUsage(ctx)
		require.Equal(t, 202, ctx.Response.StatusCode(), string(ctx.Response.Body()))
		require.NotNil(t, loggingPlugin.postResponse)
		require.Equal(t, configstore.TranscriptionUsageProvider, loggingPlugin.postResponse.TranscriptionResponse.ExtraFields.Provider)
	}
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":1,"turns":1,"outcome":"completed","session_id":"s","seq":1,"model":"transcription/later-asr"}`, "sk-bf-invalid", "bad-vk")
	h.recordUsage(ctx)
	require.Equal(t, 401, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestTranscriptionModelDetailsExactBeforePagination(t *testing.T) {
	SetLogger(testLogger{})
	config := transcriptionUsageTestConfig(t)
	for _, name := range []string{"asr", "asr-extra", "Asr"} {
		require.NoError(t, configstore.EnsureTranscriptionModel(context.Background(), config.ConfigStore, name))
	}
	h := providerHandlerForTest(configstore.TranscriptionUsageProvider, nil, []string{"asr-extra", "Asr", "asr"}, []string{"asr-extra", "Asr", "asr"})
	h.inMemoryStore.ModelCatalog = config.ModelCatalog
	h.dbStore = config.ConfigStore
	ctx := newTestRequestCtx("")
	ctx.Request.SetRequestURI("/api/models/details?provider=transcription&query=asr&exact=true&unfiltered=true&limit=1")
	h.listModelDetails(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	var response ListModelDetailsResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &response))
	require.Equal(t, 1, response.Total)
	require.Len(t, response.Models, 1)
	require.Equal(t, "asr", response.Models[0].Name)
	require.True(t, *response.Models[0].PricingConfigured)
	require.Equal(t, "stt", response.Models[0].UsageKind)
}

func TestTranscriptionUsageRejectsMissingOrChangedRegistration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		mutate func(configstore.ConfigStore) error
	}{
		{"unknown model", 400, func(s configstore.ConfigStore) error {
			return s.DB().Where("name = ?", "qwen3-asr").Delete(&tables.TableModel{}).Error
		}},
		{"provider deleted", 400, func(s configstore.ConfigStore) error {
			return s.DeleteProvider(context.Background(), configstore.TranscriptionUsageProvider)
		}},
		{"provider changed to inference", 503, func(s configstore.ConfigStore) error {
			return s.DB().Model(&tables.TableProvider{}).Where("name = ?", "transcription").Update("custom_provider_config_json", `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{"transcription":true}}`).Error
		}},
		{"inference URL configured", 503, func(s configstore.ConfigStore) error {
			return s.DB().Model(&tables.TableProvider{}).Where("name = ?", "transcription").Update("network_config_json", `{"base_url":"https://example.invalid"}`).Error
		}},
		{"pricing missing", 503, func(s configstore.ConfigStore) error {
			return s.DB().Where("provider = ?", "transcription").Delete(&tables.TableModelPricing{}).Error
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			h, logPlugin, governancePlugin := newTranscriptionUsageTestHandler(t, &events)
			require.NoError(t, tc.mutate(h.config.ConfigStore))
			ctx := newTranscriptionUsageTestContext(`{"audio_ms":1,"turns":1,"outcome":"completed","session_id":"s","seq":0,"model":"transcription/qwen3-asr"}`, "vk", "rejected")
			h.recordUsage(ctx)
			require.Equal(t, tc.status, ctx.Response.StatusCode(), string(ctx.Response.Body()))
			require.Zero(t, logPlugin.preCalls)
			require.Zero(t, governancePlugin.preCalls)
		})
	}
}
