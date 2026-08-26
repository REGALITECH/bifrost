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

func TestFishRealtime_SessionUpdateParameters(t *testing.T) {
	provider := &FishAudioProvider{}
	raw := []byte(`{
		"type": "session.update",
		"session": {
			"voice": "fishaudio/test-voice-id",
			"output_audio_type": "pcm",
			"temperature": 0.7,
			"sample_rate": 16000,
			"latency": "balanced",
			"normalize": false,
			"top_p": 0.9,
			"speed": 1.0,
			"volume": 0.0,
			"chunk_length": 200,
			"max_new_tokens": 1024,
			"repetition_penalty": 1.2,
			"min_chunk_length": 50,
			"condition_on_previous_chunks": true,
			"early_stop_threshold": 1.0
		}
	}`)

	event, err := schemas.ParseRealtimeEvent(raw)
	if err != nil {
		t.Fatalf("ParseRealtimeEvent() error = %v", err)
	}
	frame, err := provider.ToProviderRealtimeEvent(event)
	if err != nil {
		t.Fatalf("ToProviderRealtimeEvent() error = %v", err)
	}

	var start FishAudioStartEvent
	if err := msgpack.Unmarshal(frame, &start); err != nil {
		t.Fatalf("failed to msgpack-decode start event: %v", err)
	}
	if start.Event != fishEventStart {
		t.Fatalf("event = %q, want %q", start.Event, fishEventStart)
	}
	req := start.Request
	if req.Format != "pcm" {
		t.Errorf("format = %q, want pcm", req.Format)
	}
	assertPointerValue(t, "reference_id", req.ReferenceID, "test-voice-id")
	assertPointerValue(t, "sample_rate", req.SampleRate, 16000)
	if req.Latency != "balanced" {
		t.Errorf("latency = %q, want balanced", req.Latency)
	}
	assertPointerValue(t, "normalize", req.Normalize, false)
	assertPointerValue(t, "temperature", req.Temperature, 0.7)
	assertPointerValue(t, "top_p", req.TopP, 0.9)
	assertPointerValue(t, "chunk_length", req.ChunkLength, 200)
	assertPointerValue(t, "max_new_tokens", req.MaxNewTokens, 1024)
	assertPointerValue(t, "repetition_penalty", req.RepetitionPenalty, 1.2)
	assertPointerValue(t, "min_chunk_length", req.MinChunkLength, 50)
	assertPointerValue(t, "condition_on_previous_chunks", req.ConditionOnPreviousChunks, true)
	assertPointerValue(t, "early_stop_threshold", req.EarlyStopThreshold, 1.0)
	if req.Prosody == nil {
		t.Fatal("prosody is nil")
	}
	assertPointerValue(t, "prosody.speed", req.Prosody.Speed, 1.0)
	assertPointerValue(t, "prosody.volume", req.Prosody.Volume, 0.0)
}

func TestFishRealtime_SessionUpdateOmitsOptionalParameters(t *testing.T) {
	provider := &FishAudioProvider{}
	event, err := schemas.ParseRealtimeEvent([]byte(`{"type":"session.update","session":{}}`))
	if err != nil {
		t.Fatalf("ParseRealtimeEvent() error = %v", err)
	}
	frame, err := provider.ToProviderRealtimeEvent(event)
	if err != nil {
		t.Fatalf("ToProviderRealtimeEvent() error = %v", err)
	}
	req, ok := decodeFishFrame(t, frame)["request"].(map[string]any)
	if !ok {
		t.Fatal("start event request is missing or invalid")
	}
	if req["format"] != "pcm" {
		t.Errorf("format = %v, want pcm", req["format"])
	}
	for _, key := range []string{
		"reference_id", "sample_rate", "latency", "normalize", "temperature", "top_p",
		"prosody", "chunk_length", "max_new_tokens", "repetition_penalty", "min_chunk_length",
		"condition_on_previous_chunks", "early_stop_threshold",
	} {
		if _, exists := req[key]; exists {
			t.Errorf("optional field %q was unexpectedly serialized", key)
		}
	}
}

func assertPointerValue[T comparable](t *testing.T, name string, got *T, want T) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is nil, want %v", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
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

func TestFishRealtime_HeadersModel(t *testing.T) {
	provider := &FishAudioProvider{}

	for _, tt := range []struct {
		name  string
		model string
		want  string
	}{
		{name: "requested model", model: "s2.1-pro-free", want: "s2.1-pro-free"},
		{name: "default model", want: fishDefaultModel},
	} {
		t.Run(tt.name, func(t *testing.T) {
			headers, err := provider.RealtimeHeaders(nil, schemas.Key{}, tt.model)
			if err != nil {
				t.Fatalf("RealtimeHeaders() error = %v", err)
			}
			if got := headers["model"]; got != tt.want {
				t.Fatalf("RealtimeHeaders() model = %q, want %q", got, tt.want)
			}
		})
	}
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
