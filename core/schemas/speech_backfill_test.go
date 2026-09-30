package schemas_test

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// "バイフロスト" is 6 runes but 18 UTF-8 bytes — a clean way to prove the unit.
const multibyteSample = "バイフロスト"

func TestBackfillParams_InputUnit(t *testing.T) {
	wantRunes := 6
	wantBytes := 18
	if got := len([]rune(multibyteSample)); got != wantRunes {
		t.Fatalf("sample rune count = %d, want %d", got, wantRunes)
	}
	if got := len(multibyteSample); got != wantBytes {
		t.Fatalf("sample byte count = %d, want %d", got, wantBytes)
	}

	newReq := func(provider schemas.ModelProvider) *schemas.BifrostSpeechRequest {
		return &schemas.BifrostSpeechRequest{
			Provider: provider,
			Input:    &schemas.SpeechInput{Input: multibyteSample},
		}
	}

	t.Run("fish audio bills per UTF-8 byte", func(t *testing.T) {
		resp := &schemas.BifrostSpeechResponse{}
		resp.BackfillParams(newReq(schemas.FishAudio))
		if resp.Usage.InputChars != wantBytes {
			t.Fatalf("Fish InputChars = %d, want %d (bytes)", resp.Usage.InputChars, wantBytes)
		}
	})

	t.Run("other providers bill per character", func(t *testing.T) {
		resp := &schemas.BifrostSpeechResponse{}
		resp.BackfillParams(newReq(schemas.OpenAI))
		if resp.Usage.InputChars != wantRunes {
			t.Fatalf("OpenAI InputChars = %d, want %d (runes)", resp.Usage.InputChars, wantRunes)
		}
	})

	t.Run("stream response uses the same unit", func(t *testing.T) {
		resp := &schemas.BifrostSpeechStreamResponse{}
		resp.BackfillParams(newReq(schemas.FishAudio))
		if resp.Usage.InputChars != wantBytes {
			t.Fatalf("Fish stream InputChars = %d, want %d (bytes)", resp.Usage.InputChars, wantBytes)
		}
	})
}
