package metronome

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestAutomaticUsageUsesPreHookUUID(t *testing.T) {
	cases := []struct {
		name string
		resp *schemas.BifrostResponse
	}{
		{"tokens", tokenIdentityResponse()},
		{"decision without idempotency key", decisionResponse(schemas.Typesafe, "jev-1.13.0", &schemas.BifrostLLMUsage{PromptTokens: 12})},
		{"emulated decision", decisionResponse(schemas.OpenAI, "test-model", &schemas.BifrostLLMUsage{PromptTokens: 12, CompletionTokens: 30})},
		{"speech tokens", speechResponse(schemas.Gemini, "tts-model", &schemas.SpeechUsage{InputTokens: 12, OutputTokens: 340})},
		{"streamed speech tokens", speechChunk(schemas.Gemini, "tts-model", schemas.SpeechStreamResponseTypeDone, &schemas.SpeechUsage{InputTokens: 12, OutputTokens: 340})},
		{"fish speech", speechResponse(schemas.FishAudio, "s2-pro", &schemas.SpeechUsage{InputChars: 54})},
		{"fish speech stream", speechChunk(schemas.FishAudio, "s2-pro", schemas.SpeechStreamResponseTypeDone, &schemas.SpeechUsage{InputChars: 54})},
		{"fish realtime", realtimeTurn(schemas.FishAudio, "s2-pro", fishTurnUsage())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1)}
			ctx := realtimeContext("vk-1", "same-request-id")
			p.PreLLMHook(ctx, nil)
			id, _ := ctx.Value(attemptKey).(string)
			if _, err := uuid.Parse(id); err != nil {
				t.Fatalf("pre-hook did not create a UUID: %v", err)
			}

			// Failed provider attempts are not usage events. The successful retry
			// and repeated final hooks retain the request's existing UUID.
			p.PostLLMHook(ctx, tc.resp, &schemas.BifrostError{Error: &schemas.ErrorField{Message: "retryable upstream failure"}})
			if len(p.queue) != 0 {
				t.Fatal("failed attempt was exported")
			}
			ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, 1)
			for range 2 {
				p.PostLLMHook(ctx, tc.resp, nil)
				select {
				case event := <-p.queue:
					if event.TransactionID != id {
						t.Fatalf("transaction ID = %q, want pre-hook UUID %q", event.TransactionID, id)
					}
				default:
					t.Fatal("successful usage was not exported")
				}
			}

			// Fallbacks rerun the pre-hook on the same context. New requests and
			// realtime turns also get fresh UUIDs even if x-request-id is reused.
			for _, next := range []*schemas.BifrostContext{ctx, realtimeContext("vk-1", "same-request-id")} {
				p.PreLLMHook(next, nil)
				nextID, _ := next.Value(attemptKey).(string)
				if nextID == id {
					t.Fatal("new pre-hook reused the previous UUID")
				}
				p.PostLLMHook(next, tc.resp, nil)
				select {
				case event := <-p.queue:
					if event.TransactionID != nextID {
						t.Fatalf("new usage ID = %q, want %q", event.TransactionID, nextID)
					}
				default:
					t.Fatal("new usage was not exported")
				}
				id = nextID
			}
		})
	}
}

func TestUsageCustomerIsAuthenticatedVirtualKeyID(t *testing.T) {
	for _, vkID := range []string{"4e1606af-85a9-4cea-8c65-faa4eacb2031", "c0b62c29-b682-4e60-bcf1-ec4214b0058d"} {
		t.Run(vkID, func(t *testing.T) {
			p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1), ctx: context.Background()}
			ctx := audioContext(vkID, "same-request-id")
			ctx.SetValue(schemas.BifrostContextKeyVirtualKey, "sk-bf-secret-must-not-be-exported")
			ctx.SetValue(schemas.BifrostContextKeyDimensions, map[string]string{"customer_id": "another-customer", "tenant_id": "another-tenant"})
			p.PreLLMHook(ctx, nil)
			response := tokenIdentityResponse()
			got, upstreamErr, err := p.PostLLMHook(ctx, response, nil)
			if err != nil || upstreamErr != nil || got != response {
				t.Fatalf("changed inference result: %v / %v", upstreamErr, err)
			}
			select {
			case event := <-p.queue:
				if event.CustomerID != vkID {
					t.Fatalf("token customer = %q, want authenticated ID %q", event.CustomerID, vkID)
				}
			default:
				t.Fatal("token usage without customer mapping was not queued")
			}
			fish, err := p.fishAudioEvent(ctx, audioUsage(t, audioBody))
			if err != nil || fish.CustomerID != vkID {
				t.Fatalf("fish customer = %q, want authenticated ID %q; error: %v", fish.CustomerID, vkID, err)
			}
		})
	}
}

func TestUsageRequiresAuthenticatedVirtualKeyID(t *testing.T) {
	for _, vkID := range []string{"", " \t\n"} {
		p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1)}
		ctx := audioContext(vkID, "request")
		ctx.SetValue(schemas.BifrostContextKeyVirtualKey, "sk-bf-secret")
		ctx.SetValue(schemas.BifrostContextKeyDimensions, map[string]string{"customer_id": "claimed-customer"})
		p.PreLLMHook(ctx, nil)
		response := tokenIdentityResponse()
		got, upstreamErr, err := p.PostLLMHook(ctx, response, nil)
		if err != nil || upstreamErr != nil || got != response || len(p.queue) != 0 {
			t.Fatalf("unauthenticated token usage exported or inference changed: %v / %v", upstreamErr, err)
		}
		if _, err := p.fishAudioEvent(ctx, audioUsage(t, audioBody)); err == nil {
			t.Fatal("unauthenticated Fish usage accepted")
		}
	}
	p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1)}
	p.PostLLMHook(nil, tokenIdentityResponse(), nil)
	if _, err := p.fishAudioEvent(nil, audioUsage(t, audioBody)); err == nil || len(p.queue) != 0 {
		t.Fatal("nil context accepted")
	}
}

func TestConfigRejectsLegacyCustomerRouting(t *testing.T) {
	for _, raw := range []string{
		`{"customer_mapping":{"vk":"customer"}}`,
		`{"customer_mapping":{}}`,
		`{"default_customer_id":"customer"}`,
		`{"default_customer_id":""}`,
	} {
		var cfg Config
		if err := json.Unmarshal([]byte(raw), &cfg); err == nil {
			t.Fatalf("legacy routing silently accepted: %s", raw)
		}
	}
}

func tokenIdentityResponse() *schemas.BifrostResponse {
	response := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Usage: &schemas.BifrostLLMUsage{PromptTokens: 10, CompletionTokens: 2}}}
	response.PopulateExtraFields(schemas.ChatCompletionRequest, schemas.OpenAI, "test-model", "test-model")
	return response
}
