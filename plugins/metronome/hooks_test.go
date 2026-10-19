package metronome

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func fishTurnUsage() *schemas.ResponsesResponseUsage {
	return &schemas.ResponsesResponseUsage{InputTokens: 54, TotalTokens: 54}
}

// A realtime turn can finalize after Cleanup closed the queue (shutdown or
// reload): hooks must neither panic on the closed channel nor queue.
func TestHooksAfterCleanupQueueNothing(t *testing.T) {
	t.Setenv("METRONOME_TEST_KEY", "test-memory-only-key")
	p, err := Init(&Config{APIKey: schemas.NewSecretVar("env.METRONOME_TEST_KEY")}, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Cleanup(); err != nil {
		t.Fatal(err)
	}
	ctx := realtimeContext("vk-1", "turn-request-1")
	p.PreLLMHook(ctx, nil)
	for _, resp := range []*schemas.BifrostResponse{
		realtimeTurn(schemas.FishAudio, "s2.1-pro", fishTurnUsage()),
		tokenIdentityResponse(),
		speechResponse(schemas.Gemini, "gemini-2.5-flash-preview-tts", &schemas.SpeechUsage{InputTokens: 12, OutputTokens: 340}),
		speechResponse(schemas.FishAudio, "s2-pro", &schemas.SpeechUsage{InputChars: 54}),
	} {
		got, upstreamErr, err := p.PostLLMHook(ctx, resp, nil)
		if err != nil || upstreamErr != nil || got != resp {
			t.Fatalf("changed result after cleanup: %v / %v", upstreamErr, err)
		}
	}
	if len(p.queue) != 0 {
		t.Fatalf("queued %d events after cleanup", len(p.queue))
	}
}

// A full queue drops the event instead of blocking the realtime relay or the
// response path while Metronome is slow or down.
func TestFullQueueDoesNotBlockHooks(t *testing.T) {
	p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2 {
			ctx := realtimeContext("vk-1", "turn-request-1")
			p.PreLLMHook(ctx, nil)
			p.PostLLMHook(ctx, realtimeTurn(schemas.FishAudio, "s2.1-pro", fishTurnUsage()), nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a full queue blocked the hook")
	}
	if len(p.queue) != 1 {
		t.Fatalf("queued %d events, want 1", len(p.queue))
	}
}

// The served provider decides between fishaudio-usage and token-billing, and
// routing metadata determines the provider/model properties.
func TestServedModelRouting(t *testing.T) {
	fallback := "claude-fallback"
	routed := func(resp *schemas.BifrostResponse, routing schemas.RoutingInfo) *schemas.BifrostResponse {
		resp.GetExtraFields().RoutingInfo = routing
		return resp
	}
	cases := []struct {
		name                       string
		resp                       *schemas.BifrostResponse
		eventType, provider, model string // eventType "": nothing is queued
	}{
		{"routed to fish", routed(realtimeTurn(schemas.OpenAI, "gpt-realtime", fishTurnUsage()),
			schemas.RoutingInfo{Provider: schemas.FishAudio, Model: "s2.1-pro"}), "fishaudio-usage", "fishaudio", "fishaudio/s2.1-pro"},
		{"routed away from fish", routed(realtimeTurn(schemas.FishAudio, "s2.1-pro", fishTurnUsage()),
			schemas.RoutingInfo{Provider: schemas.OpenAI, Model: "gpt-realtime"}), "", "", ""},
		{"key alias", routed(realtimeTurn(schemas.FishAudio, "voice-alias", fishTurnUsage()),
			schemas.RoutingInfo{Provider: schemas.FishAudio, Model: "voice-alias", ResolvedKeyAlias: &schemas.ResolvedKeyAlias{ModelID: "s2.1-pro"}}),
			"fishaudio-usage", "fishaudio", "fishaudio/s2.1-pro"},
		// Fish speech must not take the token path, where it has no tokens and would be lost.
		{"speech routed to fish", routed(speechResponse(schemas.OpenAI, "tts-1", &schemas.SpeechUsage{InputChars: 54}),
			schemas.RoutingInfo{Provider: schemas.FishAudio, Model: "s2-pro"}), "fishaudio-usage", "fishaudio", "fishaudio/s2-pro"},
		{"server-side fallback", routed(tokenIdentityResponse(),
			schemas.RoutingInfo{Provider: schemas.Anthropic, Model: "claude-primary", ServerSideFallbackModel: &fallback}),
			"token-billing", "anthropic", "anthropic/claude-fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1)}
			ctx := realtimeContext("vk-1", "turn-request-1")
			p.PreLLMHook(ctx, nil)
			p.PostLLMHook(ctx, tc.resp, nil)
			if tc.eventType == "" {
				if len(p.queue) != 0 {
					t.Fatalf("exported: %+v", <-p.queue)
				}
				return
			}
			if len(p.queue) != 1 {
				t.Fatalf("queued %d events, want 1", len(p.queue))
			}
			event := <-p.queue
			var provider, model string
			switch props := event.Properties.(type) {
			case fishAudioProperties:
				provider, model = props.Provider, props.Model
			case TokenUsage:
				provider, model = props.Provider, props.Model
			}
			if event.EventType != tc.eventType || provider != tc.provider || model != tc.model {
				t.Fatalf("exported %s %s/%s, want %s %s/%s", event.EventType, provider, model, tc.eventType, tc.provider, tc.model)
			}
		})
	}
}
