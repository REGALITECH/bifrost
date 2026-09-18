package metronome

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestUsageCustomerIsAuthenticatedVirtualKeyID(t *testing.T) {
	for _, vkID := range []string{"4e1606af-85a9-4cea-8c65-faa4eacb2031", "c0b62c29-b682-4e60-bcf1-ec4214b0058d"} {
		t.Run(vkID, func(t *testing.T) {
			p := &Plugin{logger: testLogger{}, queue: make(chan Event[TokenUsage], 1), ctx: context.Background()}
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
		p := &Plugin{logger: testLogger{}, queue: make(chan Event[TokenUsage], 1)}
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
	p := &Plugin{logger: testLogger{}, queue: make(chan Event[TokenUsage], 1)}
	p.PostLLMHook(nil, tokenIdentityResponse(), nil)
	if _, err := p.fishAudioEvent(nil, audioUsage(t, audioBody)); err == nil || len(p.queue) != 0 {
		t.Fatal("nil context accepted")
	}
}

func TestConfigRejectsLegacyCustomerRouting(t *testing.T) {
	for _, raw := range []string{
		`{"customer_mapping":{"vk":"customer"}}`,
		`{"default_customer_id":"customer"}`,
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
