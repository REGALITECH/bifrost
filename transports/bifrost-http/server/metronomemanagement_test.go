package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/fasthttp/router"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestMetronomeManagementAPI(t *testing.T) {
	ctx := context.Background()
	log := bifrost.NewNoOpLogger()
	lib.SetLogger(log)
	logger = log
	handlers.SetLogger(log)
	store, err := configstore.NewConfigStore(ctx, &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "config.db")}}, log)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(ctx) })
	s := &BifrostHTTPServer{Config: &lib.Config{ConfigStore: store}}
	s.Client, err = bifrost.Init(ctx, schemas.BifrostConfig{Account: lib.NewBaseAccount(s.Config), Logger: log})
	require.NoError(t, err)
	t.Cleanup(s.Client.Shutdown)
	r := router.New()
	handlers.NewPluginsHandler(s, store).RegisterRoutes(r)
	call := func(method, path, body string) *fasthttp.Response {
		t.Helper()
		c := &fasthttp.RequestCtx{}
		c.Init(&fasthttp.Request{}, nil, nil)
		c.Request.Header.SetMethod(method)
		c.Request.SetRequestURI(path)
		c.Request.SetBodyString(body)
		r.Handler(c)
		return &c.Response
	}

	t.Run("discover absent builtin", func(t *testing.T) {
		res := call("GET", "/api/plugins", "")
		require.Equal(t, 200, res.StatusCode())
		require.Contains(t, string(res.Body()), `"name":"metronome"`)
		res = call("GET", "/api/plugins/metronome", "")
		require.Equal(t, 200, res.StatusCode())
		var plugin handlers.PluginResponse
		require.NoError(t, json.Unmarshal(res.Body(), &plugin))
		require.False(t, plugin.IsCustom)
		require.False(t, plugin.Enabled)
	})
	t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "")
	t.Run("disabled save preserves the environment reference", func(t *testing.T) {
		res := call("PUT", "/api/plugins/metronome", `{"enabled":false,"config":{"api_key":"env.METRONOME_MANAGEMENT_TEST_KEY","dry_run":false}}`)
		require.Equal(t, 200, res.StatusCode(), string(res.Body()))
		stored, err := store.GetPlugin(ctx, "metronome")
		require.NoError(t, err)
		require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", stored.Config.(map[string]any)["api_key"])
		require.False(t, stored.IsCustom)
	})
	t.Run("missing process environment is an error", func(t *testing.T) {
		res := call("PUT", "/api/plugins/metronome", `{"enabled":true}`)
		require.Equal(t, 500, res.StatusCode())
		require.Contains(t, string(res.Body()), "metronome api_key is required for live delivery")
		res = call("GET", "/api/plugins/metronome", "")
		var plugin handlers.PluginResponse
		require.NoError(t, json.Unmarshal(res.Body(), &plugin))
		require.True(t, plugin.Enabled)
		require.Equal(t, "error", plugin.Status.Status)
		require.NotEmpty(t, plugin.Status.Logs)
	})
	t.Run("literal key never reaches database", func(t *testing.T) {
		res := call("PUT", "/api/plugins/metronome", `{"enabled":false,"config":{"api_key":"test-literal-secret"}}`)
		require.Equal(t, 400, res.StatusCode())
		require.NotContains(t, string(res.Body()), "test-literal-secret")
		stored, err := store.GetPlugin(ctx, "metronome")
		require.NoError(t, err)
		require.NotContains(t, stored.ConfigJSON, "test-literal-secret")
	})
	t.Run("case aliases never reach database", func(t *testing.T) {
		for _, body := range []string{
			`{"enabled":false,"config":{"API_KEY":"test-case-alias-secret"}}`,
			`{"enabled":false,"config":{"Api_Key":"test-case-alias-secret"}}`,
			`{"enabled":false,"config":{"api_key":"env.METRONOME_MANAGEMENT_TEST_KEY","API_KEY":"test-case-alias-secret"}}`,
			`{"enabled":false,"config":{"API_KEY":{"ref":"env.METRONOME_MANAGEMENT_TEST_KEY","value":"test-case-alias-secret"}}}`,
		} {
			res := call("PUT", "/api/plugins/metronome", body)
			require.Equal(t, 400, res.StatusCode())
			require.NotContains(t, string(res.Body()), "test-case-alias-secret")
			stored, err := store.GetPlugin(ctx, "metronome")
			require.NoError(t, err)
			require.NotContains(t, stored.ConfigJSON, "test-case-alias-secret")
			res = call("GET", "/api/plugins/metronome", "")
			require.NotContains(t, string(res.Body()), "test-case-alias-secret")
		}
	})
	t.Run("enable reload failure retry and disable", func(t *testing.T) {
		t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "test-memory-only-key")
		res := call("PUT", "/api/plugins/metronome", `{"enabled":true}`)
		require.Equal(t, 200, res.StatusCode(), string(res.Body()))
		require.Contains(t, s.GetLoadedPluginNames(), "metronome")
		res = call("GET", "/api/plugins/metronome", "")
		var plugin handlers.PluginResponse
		require.NoError(t, json.Unmarshal(res.Body(), &plugin))
		require.Equal(t, "active", plugin.Status.Status)
		require.False(t, plugin.IsCustom)
		require.NotContains(t, string(res.Body()), "test-memory-only-key")
		stored, err := store.GetPlugin(ctx, "metronome")
		require.NoError(t, err)
		require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", stored.Config.(map[string]any)["api_key"])
		require.NotContains(t, stored.ConfigJSON, "test-memory-only-key")

		// A failed reload leaves the old instance running; the response must say so.
		t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "")
		res = call("PUT", "/api/plugins/metronome", `{"enabled":true}`)
		require.Equal(t, 500, res.StatusCode())
		res = call("GET", "/api/plugins/metronome", "")
		require.NoError(t, json.Unmarshal(res.Body(), &plugin))
		require.Equal(t, "error", plugin.Status.Status)
		require.Contains(t, plugin.Status.Logs, "the previous plugin instance is still running with its previous configuration")
		t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "test-memory-only-key")
		res = call("PUT", "/api/plugins/metronome", `{"enabled":true}`)
		require.Equal(t, 200, res.StatusCode(), string(res.Body()))
		res = call("PUT", "/api/plugins/metronome", `{"enabled":false}`)
		require.Equal(t, 200, res.StatusCode(), string(res.Body()))
		require.NotContains(t, s.GetLoadedPluginNames(), "metronome")
		res = call("GET", "/api/plugins/metronome", "")
		require.NoError(t, json.Unmarshal(res.Body(), &plugin))
		require.False(t, plugin.Enabled)
		require.Equal(t, "disabled", plugin.Status.Status)
	})
}

func TestMetronomeManagementMarshallerBeforeLoading(t *testing.T) {
	s := &BifrostHTTPServer{Config: &lib.Config{}}
	t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "test-memory-only-key")
	raw := map[string]any{"api_key": map[string]any{"ref": "env.METRONOME_MANAGEMENT_TEST_KEY", "type": "env", "value": "test-memory-only-key"}}
	stored, err := s.NormalizePluginConfig("metronome", raw)
	require.NoError(t, err)
	require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", stored["api_key"])
	raw["API_KEY"] = "test-uppercase-secret"
	redacted, err := s.ExpandPluginConfigForAPI("metronome", raw)
	require.NoError(t, err)
	data, err := json.Marshal(redacted)
	require.NoError(t, err)
	require.NotContains(t, string(data), "test-memory-only-key")
	require.NotContains(t, string(data), "test-uppercase-secret")
}
