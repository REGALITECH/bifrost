package metronome

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestTokenUsageStillUsesLLMQueue(t *testing.T) {
	p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 4), ctx: context.Background()}
	ctx := audioContext("vk-1", "request")
	p.PreLLMHook(ctx, nil)
	response := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Usage: &schemas.BifrostLLMUsage{
		PromptTokens: 100, CompletionTokens: 10, PromptTokensDetails: &schemas.ChatPromptTokensDetails{CachedReadTokens: 20, CachedWriteTokens: 5},
	}}}
	response.PopulateExtraFields(schemas.ChatCompletionRequest, schemas.OpenAI, "test-model", "test-model")
	_, _, err := p.PostLLMHook(ctx, response, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.queue:
		usage, _ := event.Properties.(TokenUsage)
		if event.CustomerID != "vk-1" || event.EventType != "token-billing" || usage.InputTokens != 75 || usage.OutputTokens != 10 || usage.CachedInputTokens != 20 || usage.CachedWriteTokens != 5 {
			t.Fatalf("changed token accounting: %+v", event)
		}
	default:
		t.Fatal("token event was not queued")
	}
	// Fish speech bills bytes: one fishaudio-usage event and no token event.
	speech := &schemas.BifrostResponse{SpeechResponse: &schemas.BifrostSpeechResponse{Usage: &schemas.SpeechUsage{InputChars: 54}}}
	speech.PopulateExtraFields(schemas.SpeechRequest, schemas.FishAudio, "s2-pro", "s2-pro")
	p.PreLLMHook(ctx, nil)
	p.PostLLMHook(ctx, speech, nil)
	if len(p.queue) != 1 {
		t.Fatalf("Fish speech queued %d events, want one", len(p.queue))
	}
	if event := <-p.queue; event.EventType != "fishaudio-usage" {
		t.Fatalf("Fish speech exported as %s", event.EventType)
	}
	response.PopulateExtraFields(schemas.ChatCompletionStreamRequest, schemas.OpenAI, "test-model", "test-model")
	p.PreLLMHook(ctx, nil)
	p.PostLLMHook(ctx, response, nil)
	if len(p.queue) != 0 {
		t.Fatal("non-final stream exported")
	}
	ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
	p.PostLLMHook(ctx, response, nil)
	if len(p.queue) != 1 {
		t.Fatal("final stream usage missing")
	}
}

// The queue carries any event type; token events must still encode exactly as before.
func TestQueuedTokenEventEncodingUnchanged(t *testing.T) {
	usage := TokenUsage{Model: "openai/test-model", Provider: "openai", InputTokens: 75, OutputTokens: 10, CachedInputTokens: 20, CachedWriteTokens: 5}
	typed, err := schemas.MarshalSorted([]Event[TokenUsage]{{TransactionID: "bifrost-1", CustomerID: "vk-1", EventType: "token-billing", Timestamp: "2026-10-14T00:00:00Z", Properties: usage}})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := schemas.MarshalSorted([]Event[any]{{TransactionID: "bifrost-1", CustomerID: "vk-1", EventType: "token-billing", Timestamp: "2026-10-14T00:00:00Z", Properties: usage}})
	if err != nil || string(queued) != string(typed) {
		t.Fatalf("token wire format changed: %s / %s", queued, typed)
	}
}

func speechResponse(provider schemas.ModelProvider, model string, usage *schemas.SpeechUsage) *schemas.BifrostResponse {
	resp := &schemas.BifrostResponse{SpeechResponse: &schemas.BifrostSpeechResponse{Audio: []byte("audio"), Usage: usage}}
	resp.PopulateExtraFields(schemas.SpeechRequest, provider, model, model)
	return resp
}

func speechChunk(provider schemas.ModelProvider, model string, kind schemas.SpeechStreamResponseType, usage *schemas.SpeechUsage) *schemas.BifrostResponse {
	resp := &schemas.BifrostResponse{SpeechStreamResponse: &schemas.BifrostSpeechStreamResponse{Type: kind, Usage: usage}}
	resp.PopulateExtraFields(schemas.SpeechStreamRequest, provider, model, model)
	return resp
}

func TestSpeechUsage(t *testing.T) {
	const gemini = "gemini-2.5-flash-preview-tts"
	geminiUsage := func() *schemas.SpeechUsage {
		return &schemas.SpeechUsage{InputTokens: 12, OutputTokens: 340, TotalTokens: 352, InputChars: 30}
	}
	fishUsage := func() *schemas.SpeechUsage { return &schemas.SpeechUsage{InputChars: 54} }
	geminiTokens := TokenUsage{Model: "gemini/" + gemini, Provider: "gemini", InputTokens: 12, OutputTokens: 340}
	fishBytes := fishAudioProperties{SchemaVersion: 1, Provider: "fishaudio", Model: "fishaudio/s2-pro", UsageSource: "speech",
		BillableBytes: 54, Outcome: "completed", TurnID: "speech-request", SubID: "speech-request"}
	cases := []struct {
		name     string
		vk       string
		final    bool
		resp     *schemas.BifrostResponse
		upstream *schemas.BifrostError
		want     any // nil: nothing is queued
	}{
		{"gemini non-stream", "vk-1", false, speechResponse(schemas.Gemini, gemini, geminiUsage()), nil, geminiTokens},
		{"gemini stream final chunk", "vk-1", true, speechChunk(schemas.Gemini, gemini, schemas.SpeechStreamResponseTypeDone, geminiUsage()), nil, geminiTokens},
		{"gemini stream delta chunk", "vk-1", false, speechChunk(schemas.Gemini, gemini, schemas.SpeechStreamResponseTypeDelta, nil), nil, nil},
		{"stream chunk with usage before the end", "vk-1", false, speechChunk(schemas.Gemini, gemini, schemas.SpeechStreamResponseTypeDone, geminiUsage()), nil, nil},
		{"gemini without tokens", "vk-1", false, speechResponse(schemas.Gemini, gemini, &schemas.SpeechUsage{InputChars: 30}), nil, nil},
		{"character-billed tts", "vk-1", false, speechResponse(schemas.OpenAI, "tts-1", &schemas.SpeechUsage{InputChars: 30}), nil, nil},
		{"gemini missing virtual key", "", false, speechResponse(schemas.Gemini, gemini, geminiUsage()), nil, nil},
		// A response with valid usage alongside an error is still a failure.
		{"failed speech", "vk-1", false, speechResponse(schemas.Gemini, gemini, geminiUsage()), &schemas.BifrostError{Error: &schemas.ErrorField{Message: "cancelled"}}, nil},
		{"failed fish speech", "vk-1", false, speechResponse(schemas.FishAudio, "s2-pro", fishUsage()), &schemas.BifrostError{Error: &schemas.ErrorField{Message: "cancelled"}}, nil},
		{"fish non-stream", "vk-1", false, speechResponse(schemas.FishAudio, "s2-pro", fishUsage()), nil, fishBytes},
		{"fish stream final chunk", "vk-1", true, speechChunk(schemas.FishAudio, "s2-pro", schemas.SpeechStreamResponseTypeDone, fishUsage()), nil, fishBytes},
		{"fish stream delta chunk", "vk-1", false, speechChunk(schemas.FishAudio, "s2-pro", schemas.SpeechStreamResponseTypeDelta, nil), nil, nil},
		{"fish stream chunk with usage before the end", "vk-1", false, speechChunk(schemas.FishAudio, "s2-pro", schemas.SpeechStreamResponseTypeDone, fishUsage()), nil, nil},
		// Fish never becomes token-billing, even if a token count appears.
		{"fish with tokens", "vk-1", false, speechResponse(schemas.FishAudio, "s2-pro", &schemas.SpeechUsage{InputTokens: 9, OutputTokens: 9, InputChars: 54}), nil, fishBytes},
		{"fish without bytes", "vk-1", false, speechResponse(schemas.FishAudio, "s2-pro", &schemas.SpeechUsage{}), nil, nil},
		{"fish missing virtual key", " ", false, speechResponse(schemas.FishAudio, "s2-pro", fishUsage()), nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 2)}
			ctx := audioContext(tc.vk, "speech-request")
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, tc.final)
			p.PreLLMHook(ctx, nil)
			got, upstreamErr, err := p.PostLLMHook(ctx, tc.resp, tc.upstream)
			if err != nil || upstreamErr != tc.upstream || got != tc.resp {
				t.Fatalf("changed speech result: %v / %v", upstreamErr, err)
			}
			if tc.want == nil {
				if len(p.queue) != 0 {
					t.Fatalf("exported: %+v", <-p.queue)
				}
				return
			}
			if len(p.queue) != 1 {
				t.Fatalf("queued %d events, want exactly one", len(p.queue))
			}
			event := <-p.queue
			if event.CustomerID != "vk-1" || event.Properties != tc.want {
				t.Fatalf("speech event = %+v, want properties %+v", event, tc.want)
			}
			wantType := "token-billing"
			if _, fish := tc.want.(fishAudioProperties); fish {
				wantType = "fishaudio-usage"
			}
			if event.EventType != wantType {
				t.Fatalf("speech event type = %s, want %s", event.EventType, wantType)
			}
		})
	}
}
