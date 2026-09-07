package bifrost

import (
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTranscriptionCustomProviderIsKeylessAndRejectsInference(t *testing.T) {
	const name schemas.ModelProvider = "transcription"
	require.False(t, IsStandardProvider(name))
	b := &Bifrost{}
	provider, err := b.createBaseProvider(name, &schemas.ProviderConfig{CustomProviderConfig: &schemas.CustomProviderConfig{
		BaseProviderType: schemas.OpenAI, IsKeyLess: true, AllowedRequests: &schemas.AllowedRequests{},
	}})
	require.NoError(t, err)
	require.Equal(t, name, provider.GetProviderKey())
	models, bErr := provider.ListModels(nil, nil, nil)
	require.Nil(t, models)
	require.NotNil(t, bErr)
	response, bErr := provider.Transcription(nil, schemas.Key{}, nil)
	require.Nil(t, response)
	require.NotNil(t, bErr)
	require.Contains(t, bErr.Error.Message, "not supported")
}
