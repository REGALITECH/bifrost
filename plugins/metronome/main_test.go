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
	// Speech continues to be excluded here: the HTTP reporting path is the only
	// source of external audio events, so there is no second billable event.
	speech := &schemas.BifrostResponse{SpeechResponse: &schemas.BifrostSpeechResponse{Usage: &schemas.SpeechUsage{InputChars: 54}}}
	speech.PopulateExtraFields(schemas.SpeechRequest, schemas.FishAudio, "s2-pro", "s2-pro")
	p.PostLLMHook(ctx, speech, nil)
	if len(p.queue) != 0 {
		t.Fatal("speech was double-exported via the token hook")
	}
	response.PopulateExtraFields(schemas.ChatCompletionStreamRequest, schemas.OpenAI, "test-model", "test-model")
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
