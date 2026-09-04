package datasheet

import (
	"context"
	"slices"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// TestApplyDerivations verifies that the Fish Audio rule mints only listed TTS
// models, copies the expected fields, and owns its cost pointer independently.
func TestApplyDerivations(t *testing.T) {
	sourceCost := 1.5e-05
	transcriptionCost := 0.0001
	pricing := map[string]Entry{
		"openrouter/fish-audio/s1": {
			Provider: "openrouter",
			Mode:     "chat",
			Options: Options{
				InputCostPerToken: &sourceCost,
			},
		},
		"openrouter/fish-audio/transcribe-1": {
			Provider: "openrouter",
			Mode:     "chat",
			Options: Options{
				InputCostPerToken: &transcriptionCost,
			},
		},
	}

	before := len(pricing)
	count := applyDerivations(pricing, builtinDerivations, nil)
	if count != 1 {
		t.Fatalf("expected one derived row, got %d", count)
	}
	if count != len(pricing)-before {
		t.Fatalf("returned count %d does not match %d minted rows", count, len(pricing)-before)
	}

	derived, ok := pricing["fishaudio/s1"]
	if !ok {
		t.Fatal("expected Fish Audio s1 to be derived")
	}
	if derived.Provider != "fishaudio" {
		t.Errorf("expected derived Fish Audio provider, got %q", derived.Provider)
	}
	if derived.Mode != "audio_speech" {
		t.Errorf("expected derived mode audio_speech, got %q", derived.Mode)
	}
	if derived.BaseModel != "s1" {
		t.Errorf("expected derived base model s1, got %q", derived.BaseModel)
	}
	if derived.InputCostPerCharacter == nil || *derived.InputCostPerCharacter != 1.5e-05 {
		t.Fatalf("expected derived input_cost_per_character=1.5e-05, got %#v", derived.InputCostPerCharacter)
	}
	if derived.InputCostPerToken != nil {
		t.Errorf("expected derived input_cost_per_token to be nil, got %#v", derived.InputCostPerToken)
	}
	if derived.InputCostPerCharacter == pricing["openrouter/fish-audio/s1"].InputCostPerToken {
		t.Fatal("expected derived cost to use a fresh pointer")
	}

	sourceCost = 9
	if *derived.InputCostPerCharacter != 1.5e-05 {
		t.Fatalf("mutating source cost changed derived value to %g", *derived.InputCostPerCharacter)
	}
	if _, ok := pricing["fishaudio/transcribe-1"]; ok {
		t.Fatal("transcribe-1 must not be derived")
	}
}

// TestApplyDerivationsSkipsInvalidSources verifies that source metadata and a
// positive input token cost are required before a target row can be minted.
func TestApplyDerivationsSkipsInvalidSources(t *testing.T) {
	positiveCost := 1.5e-05
	zeroCost := 0.0
	tests := []struct {
		name     string
		provider string
		mode     string
		cost     *float64
	}{
		{name: "wrong provider", provider: "fishaudio", mode: "chat", cost: &positiveCost},
		{name: "wrong mode", provider: "openrouter", mode: "audio_speech", cost: &positiveCost},
		{name: "nil cost", provider: "openrouter", mode: "chat", cost: nil},
		{name: "zero cost", provider: "openrouter", mode: "chat", cost: &zeroCost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricing := map[string]Entry{
				"openrouter/fish-audio/s1": {
					Provider: tt.provider,
					Mode:     tt.mode,
					Options: Options{
						InputCostPerToken: tt.cost,
					},
				},
			}

			if count := applyDerivations(pricing, builtinDerivations, nil); count != 0 {
				t.Fatalf("expected no derived rows, got %d", count)
			}
			if _, ok := pricing["fishaudio/s1"]; ok {
				t.Fatal("invalid source unexpectedly produced Fish Audio s1")
			}
		})
	}
}

// TestApplyDerivationsNativeWins verifies that a datasheet-native target row
// is neither overwritten nor included in the derived-row count.
func TestApplyDerivationsNativeWins(t *testing.T) {
	sourceCost := 1.5e-05
	nativeCost := 2.5e-05
	pricing := map[string]Entry{
		"openrouter/fish-audio/s1": {
			Provider: "openrouter",
			Mode:     "chat",
			Options: Options{
				InputCostPerToken: &sourceCost,
			},
		},
		"fishaudio/s1": {
			Provider:  "fishaudio",
			Mode:      "audio_speech",
			BaseModel: "native-s1",
			Options: Options{
				InputCostPerCharacter: &nativeCost,
			},
		},
	}

	if count := applyDerivations(pricing, builtinDerivations, nil); count != 0 {
		t.Fatalf("expected native target not to be counted, got %d", count)
	}
	got := pricing["fishaudio/s1"]
	if got.BaseModel != "native-s1" {
		t.Fatalf("native target was overwritten: base model is %q", got.BaseModel)
	}
	if got.InputCostPerCharacter != &nativeCost || *got.InputCostPerCharacter != 2.5e-05 {
		t.Fatalf("native target cost was changed: %#v", got.InputCostPerCharacter)
	}
}

// TestApplyDerivationsEmpty verifies that an empty pricing map and nil logger
// are accepted without adding rows or panicking.
func TestApplyDerivationsEmpty(t *testing.T) {
	pricing := make(map[string]Entry)
	if count := applyDerivations(pricing, builtinDerivations, nil); count != 0 {
		t.Fatalf("expected no derived rows from an empty map, got %d", count)
	}
}

// TestLoadFromLocalFilesWithDerivations verifies that URL loading applies the
// built-in Fish Audio rule while preserving the source OpenRouter rows.
func TestLoadFromLocalFilesWithDerivations(t *testing.T) {
	logger := bifrost.NewDefaultLogger(schemas.LogLevelWarn)
	ctx := context.Background()
	store := New(nil, logger, Config{
		URL:                fileURL(t, "testdata/pricing-openrouter-fishaudio.json"),
		ModelParametersURL: fileURL(t, "testdata/model-parameters.json"),
	})

	if err := store.LoadFromURLIntoMemory(ctx); err != nil {
		t.Fatalf("LoadFromURLIntoMemory with derivations failed: %v", err)
	}

	s1 := store.Get("s1", schemas.FishAudio, schemas.SpeechRequest)
	if s1 == nil {
		t.Fatal("expected derived Fish Audio speech pricing for s1, got nil")
	}
	if s1.InputCostPerCharacter == nil || *s1.InputCostPerCharacter != 1.5e-05 {
		t.Fatalf("expected s1 input_cost_per_character=1.5e-05, got %#v", s1.InputCostPerCharacter)
	}
	if store.Get("s2-pro", schemas.FishAudio, schemas.SpeechStreamRequest) == nil {
		t.Fatal("expected derived Fish Audio streaming speech pricing for s2-pro")
	}
	if store.Get("transcribe-1", schemas.FishAudio, schemas.TranscriptionRequest) != nil {
		t.Fatal("did not expect Fish Audio transcription pricing for transcribe-1")
	}
	if store.Get("fish-audio/s1", schemas.OpenRouter, schemas.ChatCompletionRequest) == nil {
		t.Fatal("expected original OpenRouter chat pricing for fish-audio/s1 to remain available")
	}

	models := store.DatasheetModelsForProvider(schemas.FishAudio)
	for _, want := range []string{"s1", "s2-pro", "s2.1-pro"} {
		if !slices.Contains(models, want) {
			t.Errorf("expected Fish Audio datasheet models to contain %q, got %v", want, models)
		}
	}
}
