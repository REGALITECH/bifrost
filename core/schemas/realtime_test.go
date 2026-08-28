package schemas

import (
	"encoding/json"
	"testing"
)

func TestIsRealtimeConversationItemEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		eventType RealtimeEventType
		want      bool
	}{
		{name: "create", eventType: RTEventConversationItemCreate, want: true},
		{name: "added", eventType: RTEventConversationItemAdded, want: true},
		{name: "created", eventType: RTEventConversationItemCreated, want: true},
		{name: "retrieved", eventType: RTEventConversationItemRetrieved, want: true},
		{name: "done", eventType: RTEventConversationItemDone, want: true},
		{name: "response done", eventType: RTEventResponseDone, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRealtimeConversationItemEventType(tt.eventType); got != tt.want {
				t.Fatalf("IsRealtimeConversationItemEventType(%q) = %v, want %v", tt.eventType, got, tt.want)
			}
		})
	}
}

func TestRealtimeCanonicalEventClassifiers(t *testing.T) {
	t.Parallel()

	userEvent := &BifrostRealtimeEvent{
		Type: RTEventConversationItemAdded,
		Item: &RealtimeItem{
			Role: "user",
			Type: "message",
		},
	}
	if !IsRealtimeUserInputEvent(userEvent) {
		t.Fatal("expected conversation.item.added user event to be classified as realtime user input")
	}
	if IsRealtimeToolOutputEvent(userEvent) {
		t.Fatal("did not expect conversation.item.added user event to be classified as realtime tool output")
	}

	toolEvent := &BifrostRealtimeEvent{
		Type: RTEventConversationItemRetrieved,
		Item: &RealtimeItem{
			Type: "function_call_output",
		},
	}
	if !IsRealtimeToolOutputEvent(toolEvent) {
		t.Fatal("expected function_call_output item to be classified as realtime tool output")
	}
	if IsRealtimeUserInputEvent(toolEvent) {
		t.Fatal("did not expect function_call_output item to be classified as realtime user input")
	}

	transcriptEvent := &BifrostRealtimeEvent{Type: RTEventInputAudioTransCompleted}
	if !IsRealtimeInputTranscriptEvent(transcriptEvent) {
		t.Fatal("expected input audio transcription completion to be classified as transcript event")
	}
	if IsRealtimeInputTranscriptEvent(&BifrostRealtimeEvent{Type: RTEventInputAudioTransDelta}) {
		t.Fatal("did not expect input audio transcription delta to be classified as transcript event")
	}
}

func TestParseRealtimeEventSessionExtraParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want map[string]any
	}{
		{
			name: "explicit extra params",
			raw:  `{"type":"session.update","session":{"extra_params":{"sample_rate":16000}}}`,
			want: map[string]any{"sample_rate": float64(16000)},
		},
		{
			name: "unknown top-level session field",
			raw:  `{"type":"session.update","session":{"sample_rate":16000}}`,
			want: map[string]any{"sample_rate": float64(16000)},
		},
		{
			name: "explicit and unknown fields with explicit collision winner",
			raw:  `{"type":"session.update","session":{"sample_rate":44100,"latency":"balanced","extra_params":{"sample_rate":16000,"normalize":false}}}`,
			want: map[string]any{"sample_rate": float64(16000), "latency": "balanced", "normalize": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event, err := ParseRealtimeEvent([]byte(tt.raw))
			if err != nil {
				t.Fatalf("ParseRealtimeEvent() error = %v", err)
			}
			if event.Session == nil {
				t.Fatal("Session is nil")
			}
			assertRealtimeExtraParams(t, event.Session.ExtraParams, tt.want)
		})
	}
}

func TestParseRealtimeEventItemExtraParams(t *testing.T) {
	t.Parallel()

	event, err := ParseRealtimeEvent([]byte(`{
		"type":"conversation.item.create",
		"item":{
			"provider_item_id":"top-level",
			"unknown_flag":true,
			"extra_params":{"provider_item_id":"explicit","custom_count":3}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseRealtimeEvent() error = %v", err)
	}
	if event.Item == nil {
		t.Fatal("Item is nil")
	}
	assertRealtimeExtraParams(t, event.Item.ExtraParams, map[string]any{
		"provider_item_id": "explicit",
		"unknown_flag":     true,
		"custom_count":     float64(3),
	})
}

func assertRealtimeExtraParams(t *testing.T, got map[string]json.RawMessage, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ExtraParams has %d entries, want %d: %v", len(got), len(want), got)
	}
	for key, wantValue := range want {
		raw, ok := got[key]
		if !ok {
			t.Errorf("ExtraParams[%q] is missing", key)
			continue
		}
		var gotValue any
		if err := Unmarshal(raw, &gotValue); err != nil {
			t.Errorf("Unmarshal(ExtraParams[%q]) error = %v", key, err)
			continue
		}
		if gotValue != wantValue {
			t.Errorf("ExtraParams[%q] = %v, want %v", key, gotValue, wantValue)
		}
	}
}
