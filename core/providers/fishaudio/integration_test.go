package fishaudio_test

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/internal/llmtests"
	"github.com/maximhq/bifrost/core/schemas"
)

// TestFishAudioIntegration is a focused, account-agnostic integration test that
// exercises a real Fish Audio key end-to-end: it lists the account's voices,
// then synthesizes both non-streaming (Speech) and streaming (SpeechStream)
// audio, asserting real audio bytes come back.
//
// It is skipped unless FISH_AUDIO_API_KEY is set. The synthesis voice is
// discovered from ListModels (the account's own voices); override it with
// FISH_AUDIO_VOICE_ID, and the model variant with FISH_AUDIO_MODEL (default
// s2-pro).
//
//	FISH_AUDIO_API_KEY=... go test ./core/providers/fishaudio/ -run TestFishAudioIntegration -v
func TestFishAudioIntegration(t *testing.T) {
	t.Parallel()
	if strings.TrimSpace(os.Getenv("FISH_AUDIO_API_KEY")) == "" {
		t.Skip("Skipping Fish Audio integration test because FISH_AUDIO_API_KEY is not set")
	}

	client, ctx, cancel, err := llmtests.SetupTest()
	if err != nil {
		t.Fatalf("Error initializing test setup: %v", err)
	}
	defer cancel()
	defer client.Shutdown()

	model := strings.TrimSpace(os.Getenv("FISH_AUDIO_MODEL"))
	if model == "" {
		model = "s2-pro"
	}

	// 1. ListModels — this both validates the key and discovers a usable voice.
	var voice string
	t.Run("ListModels", func(t *testing.T) {
		bctx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
		resp, berrResp := client.ListModelsRequest(bctx, &schemas.BifrostListModelsRequest{
			Provider: schemas.FishAudio,
		})
		if berrResp != nil {
			t.Fatalf("ListModels failed (is the FISH_AUDIO_API_KEY valid?): %s", bifrostErrMessage(berrResp))
		}
		t.Logf("Fish Audio returned %d voice model(s)", len(resp.Data))

		if override := strings.TrimSpace(os.Getenv("FISH_AUDIO_VOICE_ID")); override != "" {
			voice = override
			return
		}
		if len(resp.Data) > 0 {
			// Model IDs are namespaced as "fishaudio/<reference_id>"; strip it.
			voice = strings.TrimPrefix(resp.Data[0].ID, string(schemas.FishAudio)+"/")
		}
	})

	if voice == "" {
		t.Skip("No Fish Audio voice available (account has no voices and FISH_AUDIO_VOICE_ID not set); skipping synthesis subtests")
	}
	t.Logf("Using voice reference_id=%q model=%q", voice, model)

	newSpeechRequest := func() *schemas.BifrostSpeechRequest {
		return &schemas.BifrostSpeechRequest{
			Provider: schemas.FishAudio,
			Model:    model,
			Input:    &schemas.SpeechInput{Input: "Hello from the Bifrost Fish Audio integration test."},
			Params: &schemas.SpeechParameters{
				VoiceConfig:    &schemas.SpeechVoiceInput{Voice: schemas.Ptr(voice)},
				ResponseFormat: "mp3",
			},
		}
	}

	// 2. Speech (non-streaming) — drives the WebSocket to completion.
	t.Run("Speech", func(t *testing.T) {
		bctx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
		resp, berrResp := client.SpeechRequest(bctx, newSpeechRequest())
		if berrResp != nil {
			t.Fatalf("Speech failed: %s", bifrostErrMessage(berrResp))
		}
		if len(resp.Audio) == 0 {
			t.Fatal("Speech returned no audio bytes")
		}
		t.Logf("Speech returned %d audio bytes", len(resp.Audio))
	})

	// 3. SpeechStream — collects incremental audio deltas.
	t.Run("SpeechStream", func(t *testing.T) {
		bctx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
		stream, bErr := client.SpeechStreamRequest(bctx, newSpeechRequest())
		if bErr != nil {
			t.Fatalf("SpeechStream failed to start: %s", bifrostErrMessage(bErr))
		}

		var totalBytes, deltas int
		sawDone := false
		for chunk := range stream {
			if chunk.BifrostError != nil {
				t.Fatalf("SpeechStream returned an error chunk: %s", bifrostErrMessage(chunk.BifrostError))
			}
			if chunk.BifrostSpeechStreamResponse == nil {
				continue
			}
			switch chunk.BifrostSpeechStreamResponse.Type {
			case schemas.SpeechStreamResponseTypeDelta:
				deltas++
				totalBytes += len(chunk.BifrostSpeechStreamResponse.Audio)
			case schemas.SpeechStreamResponseTypeDone:
				sawDone = true
			}
		}

		if deltas == 0 || totalBytes == 0 {
			t.Fatalf("SpeechStream produced no audio (deltas=%d, bytes=%d)", deltas, totalBytes)
		}
		if !sawDone {
			t.Error("SpeechStream did not emit a terminal done event")
		}
		t.Logf("SpeechStream produced %d delta(s), %d audio bytes total", deltas, totalBytes)
	})
}

// bifrostErrMessage extracts a human-readable message from a BifrostError.
func bifrostErrMessage(err *schemas.BifrostError) string {
	if err == nil {
		return ""
	}
	if err.Error != nil {
		return err.Error.Message
	}
	return "unknown error"
}
