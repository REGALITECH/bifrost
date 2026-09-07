package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestTranscriptionModelManagementAndUsage(t *testing.T) {
	events := []string{}
	h, _, _ := newTranscriptionUsageTestHandler(t, &events)
	r := router.New()
	h.RegisterRoutes(r)
	h.RegisterModelRoutes(r)
	request := func(method, path, body string, status int) *fasthttp.RequestCtx {
		ctx := newTranscriptionUsageTestContext(body, "vk-test", "usage-new")
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(path)
		r.Handler(ctx)
		require.Equal(t, status, ctx.Response.StatusCode(), string(ctx.Response.Body()))
		return ctx
	}
	usage := `{"audio_ms":2500,"turns":1,"outcome":"completed","session_id":"s","seq":1,"model":"transcription/New-asr"}`
	request("POST", transcriptionUsagePath, usage, 400)
	request("GET", "/api/transcription/models/New-asr", "", 404)
	request("POST", "/api/transcription/models", `{"model":"New-asr"}`, 200)
	request("POST", transcriptionUsagePath, usage, 202)
	request("PUT", "/api/transcription/models/New-asr", `{"enabled":false}`, 200)
	request("POST", transcriptionUsagePath, usage, 400)
	ctx := request("POST", "/api/transcription/models", `{"model":"New-asr"}`, 200)
	var state configstore.TranscriptionModelState
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &state))
	require.False(t, state.Enabled)
	request("PUT", "/api/transcription/models/New-asr", `{"enabled":true}`, 200)
	request("POST", transcriptionUsagePath, usage, 202)
	request("GET", "/api/transcription/models", "", 200)
	for _, body := range []string{`{"model":" New-asr"}`, `{"model":"transcription/New-asr"}`, `{"model":"New-asr","enabled":true}`, `null`, `{} {}`} {
		request("POST", "/api/transcription/models", body, 400)
	}
	request("PUT", "/api/transcription/models/New-asr", `{}`, 400)
	request("PUT", "/api/transcription/models/missing", `{"enabled":true}`, 404)
	// Missing pricing is visible to reconciliation and blocks accounting.
	require.NoError(t, h.config.ConfigStore.DB().Where("provider = ? AND model = ?", "transcription", "New-asr").Delete(&tables.TableModelPricing{}).Error)
	request("POST", transcriptionUsagePath, usage, 503)
	ctx = request("GET", "/api/transcription/models/New-asr", "", 200)
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &state))
	require.False(t, state.PricingConfigured)
	request("POST", "/api/transcription/models", `{"model":"New-asr"}`, 200)
	request("POST", transcriptionUsagePath, usage, 202)
	// No external inference configuration was created as a side effect.
	providers, err := h.config.ConfigStore.GetProviders(context.Background())
	require.NoError(t, err)
	require.Empty(t, providers)
}

func TestTranscriptionModelRoutesApplyManagementMiddleware(t *testing.T) {
	events := []string{}
	h, _, _ := newTranscriptionUsageTestHandler(t, &events)
	r := router.New()
	auth, err := InitAuthMiddleware(h.config.ConfigStore, nil, nil)
	require.NoError(t, err)
	auth.UpdateAuthConfig(&configstore.AuthConfig{IsEnabled: true, AdminUserName: schemas.NewSecretVar("admin"), AdminPassword: schemas.NewSecretVar("test-password")})
	h.RegisterModelRoutes(r, auth.APIMiddleware())
	ctx := newTranscriptionUsageTestContext(`{"model":"unauthorized"}`, "vk-only", "")
	ctx.Request.SetRequestURI("/api/transcription/models")
	r.Handler(ctx)
	require.Equal(t, 401, ctx.Response.StatusCode())
	_, err = configstore.GetTranscriptionModel(context.Background(), h.config.ConfigStore, "unauthorized")
	require.Error(t, err)
}

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
		require.Equal(t, schemas.Transcription, loggingPlugin.postResponse.TranscriptionResponse.ExtraFields.Provider)
	}
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":1,"turns":1,"outcome":"completed","session_id":"s","seq":1,"model":"transcription/later-asr"}`, "sk-bf-invalid", "bad-vk")
	h.recordUsage(ctx)
	require.Equal(t, 401, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestTranscriptionProviderManagementRejectsInference(t *testing.T) {
	h := &ProviderHandler{}
	ctx := newTestRequestCtx(`{"provider":"transcription","custom_provider_config":{"base_provider_type":"vllm"}}`)
	h.addProvider(ctx)
	require.Equal(t, 400, ctx.Response.StatusCode())
}
