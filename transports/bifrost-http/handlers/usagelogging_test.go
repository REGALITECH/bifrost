package handlers

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// newUsageLoggingPlugin starts the real logging plugin on a fresh SQLite log
// store, pricing with catalog.
func newUsageLoggingPlugin(t *testing.T, catalog *modelcatalog.ModelCatalog) (*logging.LoggerPlugin, logstore.LogStore) {
	t.Helper()
	store, err := logstore.NewLogStore(context.Background(), &logstore.Config{
		Enabled: true,
		Type:    logstore.LogStoreTypeSQLite,
		Config:  &logstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "logs.db")},
	}, testLogger{})
	require.NoError(t, err)
	plugin, err := logging.Init(context.Background(), &logging.Config{}, testLogger{}, store, nil, catalog, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plugin.Cleanup() })
	return plugin, store
}

// requireSingleUsageLog waits for the logging plugin to flush and returns the
// only row in the log store, which must have ID requestID.
func requireSingleUsageLog(t *testing.T, plugin *logging.LoggerPlugin, store logstore.LogStore, requestID string) *logstore.Log {
	t.Helper()
	require.NoError(t, plugin.Cleanup())
	rows, err := store.FindAll(context.Background(), map[string]any{}, "id")
	require.NoError(t, err)
	require.Len(t, rows, 1, "a retried usage report must be logged once")
	require.Equal(t, requestID, rows[0].ID)
	entry, err := store.FindByID(context.Background(), requestID)
	require.NoError(t, err)
	return entry
}

// TestFishAudioUsageWithRealLoggingAndGovernance sends one Fish Audio usage
// report twice with the same x-request-id through the real logging and
// governance plugins: it is logged once, with the billable bytes as prompt
// tokens and priced per byte, and billed once.
func TestFishAudioUsageWithRealLoggingAndGovernance(t *testing.T) {
	SetLogger(testLogger{})
	catalog := modelcatalog.NewTestCatalog(nil)
	provider := string(schemas.FishAudio)
	require.NoError(t, catalog.SetPricingOverrides([]configstoreTables.TablePricingOverride{{
		ID:               "fish-s2-pro",
		ScopeKind:        "provider",
		ProviderID:       &provider,
		MatchType:        "exact",
		Pattern:          "s2-pro",
		RequestTypes:     []schemas.RequestType{schemas.SpeechRequest},
		PricingPatchJSON: `{"input_cost_per_character":0.000015}`,
	}}))
	loggingPlugin, store := newUsageLoggingPlugin(t, catalog)
	gov := newUsageBillingGovernance(t, schemas.FishAudio)
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}, ModelCatalog: catalog}
	plugins := []schemas.BasePlugin{loggingPlugin, gov}
	config.BasePlugins.Store(&plugins)
	handler := NewFishAudioUsageHandler(config, logging.PluginName, governance.PluginName)

	const body = `{"billable_bytes":54,"audio_ms":1250,"outcome":"cache_hit","turn_id":"turn-1","sub_id":"sub-1","model":"fishaudio/s2-pro"}`
	for range 2 {
		ctx := newFishAudioUsageTestContext(body, usageBillingVKA.value, "fish-report-1")
		handler.recordUsage(ctx)
		require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}

	entry := requireSingleUsageLog(t, loggingPlugin, store, "fish-report-1")
	assert.Equal(t, string(schemas.FishAudio), entry.Provider)
	assert.Equal(t, "s2-pro", entry.Model)
	assert.Equal(t, "success", entry.Status)
	assert.Equal(t, 54, entry.PromptTokens)
	require.NotNil(t, entry.VirtualKeyID)
	assert.Equal(t, usageBillingVKA.id, *entry.VirtualKeyID)
	require.NotNil(t, entry.Cost, "the report must be priced")
	assert.InDelta(t, 54*0.000015, *entry.Cost, 1e-12)
	assert.Equal(t, map[string]int64{usageBillingVKA.id: 1, usageBillingVKB.id: 0}, billedRequests(t, gov))
}
