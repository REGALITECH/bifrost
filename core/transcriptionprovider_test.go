package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestTranscriptionProviderRejectsInferenceConfiguration(t *testing.T) {
	b := &Bifrost{}
	for _, config := range []*schemas.ProviderConfig{{}, {CustomProviderConfig: &schemas.CustomProviderConfig{BaseProviderType: schemas.VLLM}}} {
		provider, err := b.createBaseProvider(schemas.Transcription, config)
		require.Nil(t, provider)
		require.ErrorContains(t, err, "reserved for usage accounting")
	}
	provider, model := schemas.ParseModelString("transcription/Asr-1", "")
	require.Equal(t, schemas.Transcription, provider)
	require.Equal(t, "Asr-1", model)
}
