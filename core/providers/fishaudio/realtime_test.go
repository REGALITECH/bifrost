package fishaudio

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/maximhq/bifrost/core/schemas"
)

// decodeFishFrame unmarshals a msgpack frame into a generic event map.
func decodeFishFrame(t *testing.T, frame json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := msgpack.Unmarshal(frame, &m); err != nil {
		t.Fatalf("failed to msgpack-decode frame: %v", err)
	}
	return m
}

func TestFishRealtime_ToProviderRealtimeEvent(t *testing.T) {
	provider := &FishAudioProvider{}

	t.Run("session.update -> start with reference_id (prefix stripped)", func(t *testing.T) {
		frame, err := provider.ToProviderRealtimeEvent(&schemas.BifrostRealtimeEvent{
			Type:    schemas.RTEventSessionUpdate,
			Session: &schemas.RealtimeSession{Voice: "fishaudio/abc123"},
		})
		if err != nil || frame == nil {
			t.Fatalf("unexpected err=%v frame=%v", err, frame)
		}
		m := decodeFishFrame(t, frame)
		if m["event"] != fishEventStart {
			t.Fatalf("event = %v, want %q", m["event"], fishEventStart)
		}
		req, _ := m["request"].(map[string]any)
		if req["reference_id"] != "abc123" {
			t.Fatalf("reference_id = %v, want abc123 (prefix stripped)", req["reference_id"])
		}
	})

	t.Run("conversation.item.create -> text", func(t *testing.T) {
		frame, err := provider.ToProviderRealtimeEvent(&schemas.BifrostRealtimeEvent{
			Type: schemas.RTEventConversationItemCreate,
			Item: &schemas.RealtimeItem{Content: json.RawMessage(`[{"type":"input_text","text":"hello world"}]`)},
		})
		if err != nil || frame == nil {
			t.Fatalf("unexpected err=%v frame=%v", err, frame)
		}
		m := decodeFishFrame(t, frame)
		if m["event"] != fishEventText {
			t.Fatalf("event = %v, want %q", m["event"], fishEventText)
		}
		if m["text"] != "hello world" {
			t.Fatalf("text = %v, want 'hello world'", m["text"])
		}
	})

	t.Run("response.create -> flush", func(t *testing.T) {
		frame, _ := provider.ToProviderRealtimeEvent(&schemas.BifrostRealtimeEvent{Type: schemas.RTEventResponseCreate})
		if m := decodeFishFrame(t, frame); m["event"] != fishEventFlush {
			t.Fatalf("event = %v, want %q", m["event"], fishEventFlush)
		}
	})

	t.Run("response.cancel -> stop", func(t *testing.T) {
		frame, _ := provider.ToProviderRealtimeEvent(&schemas.BifrostRealtimeEvent{Type: schemas.RTEventResponseCancel})
		if m := decodeFishFrame(t, frame); m["event"] != fishEventStop {
			t.Fatalf("event = %v, want %q", m["event"], fishEventStop)
		}
	})

	t.Run("unmapped event -> no upstream frame", func(t *testing.T) {
		frame, err := provider.ToProviderRealtimeEvent(&schemas.BifrostRealtimeEvent{Type: schemas.RTEventSessionCreated})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(frame) != 0 {
			t.Fatalf("expected nil frame for unmapped event, got %v", frame)
		}
	})
}

func TestFishRealtime_ToBifrostRealtimeEvent(t *testing.T) {
	provider := &FishAudioProvider{}

	t.Run("audio -> response.audio.delta with base64 RawData", func(t *testing.T) {
		audio := []byte{0x01, 0x02, 0x03, 0x04}
		frame, _ := msgpack.Marshal(FishAudioServerEvent{Event: fishEventAudio, Audio: audio})

		event, err := provider.ToBifrostRealtimeEvent(frame)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if event.Type != schemas.RTEventResponseAudioDelta {
			t.Fatalf("type = %v, want response.audio.delta", event.Type)
		}
		wantB64 := base64.StdEncoding.EncodeToString(audio)
		if event.Delta == nil || event.Delta.Audio != wantB64 {
			t.Fatalf("delta audio = %v, want %s", event.Delta, wantB64)
		}
		// RawData is the canonical client JSON.
		var client map[string]any
		if err := json.Unmarshal(event.RawData, &client); err != nil {
			t.Fatalf("RawData not valid JSON: %v", err)
		}
		if client["type"] != string(schemas.RTEventResponseAudioDelta) || client["delta"] != wantB64 {
			t.Fatalf("client JSON = %v", client)
		}
	})

	t.Run("finish(stop) -> response.audio.done", func(t *testing.T) {
		frame, _ := msgpack.Marshal(FishAudioServerEvent{Event: fishEventFinish, Reason: fishFinishReasonStop})
		event, _ := provider.ToBifrostRealtimeEvent(frame)
		if event.Type != schemas.RTEventResponseAudioDone {
			t.Fatalf("type = %v, want response.audio.done", event.Type)
		}
	})

	t.Run("finish(error) -> error", func(t *testing.T) {
		frame, _ := msgpack.Marshal(FishAudioServerEvent{Event: fishEventFinish, Reason: fishFinishReasonError, Message: "boom"})
		event, _ := provider.ToBifrostRealtimeEvent(frame)
		if event.Type != schemas.RTEventError || event.Error == nil || event.Error.Message != "boom" {
			t.Fatalf("expected error event with message 'boom', got %+v", event)
		}
	})
}

func TestFishRealtime_Capabilities(t *testing.T) {
	provider := &FishAudioProvider{}
	if !provider.SupportsRealtimeAPI() {
		t.Fatal("SupportsRealtimeAPI should be true")
	}
	if !provider.RealtimeUsesBinaryProtocol() {
		t.Fatal("RealtimeUsesBinaryProtocol should be true")
	}
	// Ensure the provider satisfies the optional interfaces.
	var _ schemas.RealtimeProvider = provider
	var _ schemas.RealtimeBinaryProvider = provider
	var _ schemas.RealtimeInputUsageProvider = provider
}

func TestFishRealtime_InputUsage(t *testing.T) {
	provider := &FishAudioProvider{}

	// "バイフロスト" = 6 runes / 18 UTF-8 bytes; Fish bills per byte.
	usage := provider.RealtimeInputUsage("バイフロスト")
	if usage == nil || usage.PromptTokens != 18 || usage.TotalTokens != 18 {
		t.Fatalf("RealtimeInputUsage = %+v, want PromptTokens/TotalTokens = 18 (bytes)", usage)
	}

	if provider.RealtimeInputUsage("") != nil {
		t.Fatal("RealtimeInputUsage(\"\") should be nil")
	}
}
