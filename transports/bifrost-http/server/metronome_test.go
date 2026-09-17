package server

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/metronome"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
)

func TestMetronomeBuiltinRegistration(t *testing.T) {
	require.True(t, lib.IsBuiltinPlugin(metronome.PluginName))
	plugin, err := InstantiatePlugin(context.Background(), metronome.PluginName, nil, map[string]any{}, &lib.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, plugin.Cleanup()) })
	require.IsType(t, &metronome.Plugin{}, plugin)
	require.Equal(t, []schemas.PluginType{schemas.PluginTypeLLM}, InferPluginTypes(plugin))
	_, err = InstantiatePlugin(context.Background(), metronome.PluginName, nil, map[string]any{"dry_run": false}, &lib.Config{})
	require.ErrorContains(t, err, "api_key")
	_, err = InstantiatePlugin(context.Background(), metronome.PluginName, nil, map[string]any{
		"customer_mapping": map[string]string{"vk": "customer"},
	}, &lib.Config{})
	require.ErrorContains(t, err, "ingest aliases")
}
