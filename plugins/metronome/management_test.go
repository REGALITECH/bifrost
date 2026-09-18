package metronome

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagementEmptyLegacyMigration(t *testing.T) {
	for _, raw := range []string{
		`{"customer_mapping":{},"default_customer_id":""}`,
		`{"customer_mapping":null,"default_customer_id":null}`,
	} {
		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		var input map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &input))
		stored, err := (&Plugin{}).MarshalConfigForStorage(input)
		require.NoError(t, err)
		require.NotContains(t, stored, "customer_mapping")
		require.NotContains(t, stored, "default_customer_id")
		require.Contains(t, input, "customer_mapping", "must not mutate caller config")
	}
}

func TestManagementPreservesPopulatedLegacyForDisabledPlugin(t *testing.T) {
	raw := map[string]any{"customer_mapping": map[string]any{"vk": "customer"}, "default_customer_id": "old-customer"}
	stored, err := (&Plugin{}).MarshalConfigForStorage(raw)
	require.NoError(t, err)
	require.Equal(t, raw, stored)
	_, err = (&Plugin{}).RedactConfig(raw)
	require.NoError(t, err, "legacy configuration must remain inspectable")
	data, err := json.Marshal(stored)
	require.NoError(t, err)
	var cfg Config
	require.ErrorContains(t, json.Unmarshal(data, &cfg), "ingest aliases")
}

func TestManagementRejectsLiteralCredentials(t *testing.T) {
	for _, key := range []any{"test-plaintext-secret", map[string]any{"value": "test-plaintext-secret"}, "vault.not-allowed", "env.", "env.INVALID NAME"} {
		_, err := (&Plugin{}).MarshalConfigForStorage(map[string]any{"api_key": key})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "test-plaintext-secret")
	}
}

func TestManagementDiscardsResolvedValueInCredentialObject(t *testing.T) {
	t.Setenv("METRONOME_MANAGEMENT_TEST_KEY", "test-runtime-secret")
	raw := map[string]any{"api_key": map[string]any{"ref": "env.METRONOME_MANAGEMENT_TEST_KEY", "type": "env", "value": "test-client-supplied-secret"}}
	stored, err := (&Plugin{}).MarshalConfigForStorage(raw)
	require.NoError(t, err)
	require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", stored["api_key"])
	data, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
}

func TestManagementRejectsNoncanonicalCredentialKeys(t *testing.T) {
	for _, name := range []string{"API_KEY", "Api_Key", "api_Key"} {
		for _, value := range []any{"test-literal-secret", "env.METRONOME_MANAGEMENT_TEST_KEY", "", nil} {
			for _, withCanonical := range []bool{false, true} {
				raw := map[string]any{name: value}
				if withCanonical {
					raw["api_key"] = "env.METRONOME_MANAGEMENT_TEST_KEY"
				}
				_, err := (&Plugin{}).MarshalConfigForStorage(raw)
				require.Error(t, err, "noncanonical key %s must be rejected even alongside api_key", name)
				require.NotContains(t, err.Error(), "test-literal-secret")
			}
		}
	}
}

func TestManagementRedactsEveryCredentialKeyVariant(t *testing.T) {
	raw := map[string]any{
		"api_key": "env.METRONOME_MANAGEMENT_TEST_KEY",
		"API_KEY": "test-uppercase-secret",
		"Api_Key": map[string]any{"ref": "env.METRONOME_OTHER_TEST_KEY", "value": "test-resolved-secret"},
		"api_Key": map[string]any{"value": "test-literal-secret"},
	}
	redacted, err := (&Plugin{}).RedactConfig(raw)
	require.NoError(t, err)
	require.Equal(t, "env.METRONOME_MANAGEMENT_TEST_KEY", redacted["api_key"])
	require.Equal(t, "<REDACTED>", redacted["API_KEY"])
	require.Equal(t, "env.METRONOME_OTHER_TEST_KEY", redacted["Api_Key"])
	require.Equal(t, "<REDACTED>", redacted["api_Key"])
	data, err := json.Marshal(redacted)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
	require.Equal(t, "test-uppercase-secret", raw["API_KEY"], "must not mutate caller config")
}
