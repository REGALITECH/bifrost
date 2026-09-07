package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestTranscriptionProviderIsKeylessAndRejectsInference(t *testing.T) {
	b := &Bifrost{}
	for _, config := range []*schemas.ProviderConfig{{CustomProviderConfig: &schemas.CustomProviderConfig{BaseProviderType: schemas.VLLM}}, {NetworkConfig: schemas.NetworkConfig{BaseURL: "http://example.invalid"}}} {
		_, err := b.createBaseProvider(schemas.Transcription, config)
		require.ErrorContains(t, err, "does not accept inference configuration")
	}
	provider, err := b.createBaseProvider(schemas.Transcription, &schemas.ProviderConfig{})
	require.NoError(t, err)
	require.Equal(t, schemas.Transcription, provider.GetProviderKey())
	models, bErr := provider.ListModels(nil, nil, nil)
	require.Nil(t, bErr)
	require.Empty(t, models.Data)
	response, bErr := provider.Transcription(nil, schemas.Key{}, nil)
	require.Nil(t, response)
	require.Equal(t, 400, *bErr.StatusCode)
	parsed, model := schemas.ParseModelString("transcription/Asr-1", "")
	require.Equal(t, schemas.Transcription, parsed)
	require.Equal(t, "Asr-1", model)
}
