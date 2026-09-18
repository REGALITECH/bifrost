package server

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
)

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
