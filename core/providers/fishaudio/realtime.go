package fishaudio

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/maximhq/bifrost/core/schemas"
)

// This file exposes Fish Audio's bidirectional tts-live WebSocket through
// Bifrost's Realtime API. Fish's wire format is MessagePack binary (not JSON),
// so the provider opts into RealtimeBinaryProvider and the transport handles
// the binary framing. The client speaks the standard OpenAI-realtime JSON
// protocol; this file maps between that and Fish's events.
//
// Client (OpenAI-realtime JSON)        -> Fish (msgpack), one client event = one frame
//   session.update                     -> StartEvent (config; voice -> reference_id)
//   conversation.item.create (text)    -> TextEvent
//   response.create                    -> FlushEvent (force synthesis)
//   response.cancel / disconnect       -> CloseEvent (stop)
//
// Fish (msgpack) -> client (canonical JSON, placed in BifrostRealtimeEvent.RawData)
//   audio        -> response.audio.delta {delta: <base64>}
//   finish(stop) -> response.audio.done
//   finish/err   -> error

// fishRealtimeModel is the model variant header used for realtime synthesis.
// The realtime interface does not surface the requested model to RealtimeHeaders,
// so v1 uses Fish's recommended default.
const fishRealtimeModel = fishDefaultModel

// SupportsRealtimeAPI reports that Fish Audio supports the realtime WebSocket.
func (provider *FishAudioProvider) SupportsRealtimeAPI() bool {
	return true
}

// RealtimeUsesBinaryProtocol tells the transport to frame upstream traffic as
// binary MessagePack and to take the client-facing payload from RawData.
func (provider *FishAudioProvider) RealtimeUsesBinaryProtocol() bool {
	return true
}

// RealtimeWebSocketURL returns the tts-live endpoint. The model variant is sent
// as a header (see RealtimeHeaders), not in the URL.
func (provider *FishAudioProvider) RealtimeWebSocketURL(_ schemas.Key, _ string) string {
	return fishWebSocketURL(provider.networkConfig.BaseURL)
}

// RealtimeHeaders returns the auth + model headers for the tts-live handshake.
func (provider *FishAudioProvider) RealtimeHeaders(_ *schemas.BifrostContext, key schemas.Key) (map[string]string, *schemas.BifrostError) {
	headers := map[string]string{"model": fishRealtimeModel}
	if value := key.Value.GetValue(); value != "" {
		headers["Authorization"] = "Bearer " + value
	}
	for k, v := range provider.networkConfig.ExtraHeaders {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "model") {
			continue
		}
		headers[k] = v
	}
	return headers, nil
}

func (provider *FishAudioProvider) RealtimeWebSocketSubprotocol() string { return "" }

func (provider *FishAudioProvider) SupportsRealtimeWebRTC() bool { return false }

func (provider *FishAudioProvider) ExchangeRealtimeWebRTCSDP(_ *schemas.BifrostContext, _ schemas.Key, _ string, _ string, _ json.RawMessage) (string, *schemas.BifrostError) {
	return "", &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     schemas.Ptr(400),
		Error:          &schemas.ErrorField{Type: schemas.Ptr("invalid_request_error"), Message: "WebRTC SDP exchange is not supported for Fish Audio"},
	}
}

func (provider *FishAudioProvider) RealtimeWebRTCDataChannelLabel() string { return "" }

// ShouldStartRealtimeTurn relies on finalize-time fallback hooks (no explicit
// turn-start signal), matching other audio providers.
func (provider *FishAudioProvider) ShouldStartRealtimeTurn(_ *schemas.BifrostRealtimeEvent) bool {
	return false
}

// RealtimeTurnFinalEvent marks audio completion (Fish finish(stop)) as the turn end.
func (provider *FishAudioProvider) RealtimeTurnFinalEvent() schemas.RealtimeEventType {
	return schemas.RTEventResponseAudioDone
}

// ShouldForwardRealtimeEvent forwards only the events the client cares about.
func (provider *FishAudioProvider) ShouldForwardRealtimeEvent(event *schemas.BifrostRealtimeEvent) bool {
	switch event.Type {
	case schemas.RTEventResponseAudioDelta, schemas.RTEventResponseAudioDone, schemas.RTEventError:
		return true
	default:
		return false
	}
}

// ShouldAccumulateRealtimeOutput is false — Fish realtime produces audio only,
// no text/transcript to accumulate.
func (provider *FishAudioProvider) ShouldAccumulateRealtimeOutput(_ schemas.RealtimeEventType) bool {
	return false
}

// RealtimeInputUsage reports the turn's billable usage as the UTF-8 byte count
// of the synthesized input text — Fish Audio bills per byte, and emits no token
// usage on its terminal event. The byte count is surfaced as input tokens so the
// per-token pricing dimension (configured as the per-byte rate) computes cost.
func (provider *FishAudioProvider) RealtimeInputUsage(inputText string) *schemas.BifrostLLMUsage {
	n := schemas.SpeechBillableInputUnits(schemas.FishAudio, inputText) // UTF-8 bytes
	if n == 0 {
		return nil
	}
	return &schemas.BifrostLLMUsage{PromptTokens: n, TotalTokens: n}
}

// ToProviderRealtimeEvent maps a client (OpenAI-realtime) event to a single Fish
// msgpack frame. Returns (nil, nil) for events that should not be forwarded
// upstream (the transport skips empty binary frames).
func (provider *FishAudioProvider) ToProviderRealtimeEvent(bifrostEvent *schemas.BifrostRealtimeEvent) (json.RawMessage, error) {
	switch bifrostEvent.Type {
	case schemas.RTEventSessionUpdate:
		return msgpack.Marshal(FishAudioStartEvent{
			Event:   fishEventStart,
			Request: fishRealtimeStartRequest(bifrostEvent.Session),
		})

	case schemas.RTEventConversationItemCreate:
		text := fishRealtimeTextFromItem(bifrostEvent.Item)
		if text == "" && bifrostEvent.Delta != nil {
			text = bifrostEvent.Delta.Text
		}
		if text == "" {
			return nil, nil // nothing to synthesize; skip
		}
		return msgpack.Marshal(FishAudioTextEvent{Event: fishEventText, Text: text})

	case schemas.RTEventResponseCreate:
		return msgpack.Marshal(FishAudioControlEvent{Event: fishEventFlush})

	case schemas.RTEventResponseCancel:
		return msgpack.Marshal(FishAudioControlEvent{Event: fishEventStop})

	default:
		// Unmapped client event — don't forward to Fish.
		return nil, nil
	}
}

// ToBifrostRealtimeEvent decodes a Fish msgpack frame into a Bifrost event and
// populates RawData with the canonical OpenAI-realtime JSON the client receives.
func (provider *FishAudioProvider) ToBifrostRealtimeEvent(providerEvent json.RawMessage) (*schemas.BifrostRealtimeEvent, error) {
	var raw FishAudioServerEvent
	if err := msgpack.Unmarshal(providerEvent, &raw); err != nil {
		return nil, fmt.Errorf("failed to decode fish audio realtime event: %w", err)
	}

	switch raw.Event {
	case fishEventAudio:
		audioB64 := base64.StdEncoding.EncodeToString(raw.Audio)
		clientJSON, _ := json.Marshal(map[string]any{
			"type":  string(schemas.RTEventResponseAudioDelta),
			"delta": audioB64,
		})
		return &schemas.BifrostRealtimeEvent{
			Type:    schemas.RTEventResponseAudioDelta,
			Delta:   &schemas.RealtimeDelta{Audio: audioB64},
			RawData: clientJSON,
		}, nil

	case fishEventFinish:
		if raw.Reason == fishFinishReasonError {
			return fishRealtimeErrorEvent(raw.Message), nil
		}
		clientJSON, _ := json.Marshal(map[string]any{"type": string(schemas.RTEventResponseAudioDone)})
		return &schemas.BifrostRealtimeEvent{
			Type:    schemas.RTEventResponseAudioDone,
			RawData: clientJSON,
		}, nil

	case fishEventError:
		return fishRealtimeErrorEvent(raw.Message), nil

	default:
		// Unknown event: produce a non-forwarded event (ShouldForwardRealtimeEvent
		// returns false for unmapped types).
		return &schemas.BifrostRealtimeEvent{Type: schemas.RealtimeEventType(raw.Event)}, nil
	}
}

// fishRealtimeStartRequest builds Fish's StartEvent config from the realtime session.
func fishRealtimeStartRequest(session *schemas.RealtimeSession) FishAudioTTSRequest {
	// Default to PCM for low-latency realtime playback.
	req := FishAudioTTSRequest{Format: "pcm"}
	if session == nil {
		return req
	}
	if voice := strings.TrimPrefix(strings.TrimSpace(session.Voice), string(schemas.FishAudio)+"/"); voice != "" {
		req.ReferenceID = schemas.Ptr(voice)
	}
	if format := normalizeFishFormat(session.OutputAudioType); format != "" {
		req.Format = format
	}
	if session.Temperature != nil {
		req.Temperature = session.Temperature
	}
	return req
}

// fishRealtimeTextFromItem extracts input text from a conversation item's content,
// supporting both the OpenAI content-block array and a plain string.
func fishRealtimeTextFromItem(item *schemas.RealtimeItem) string {
	if item == nil || len(item.Content) == 0 {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(item.Content, &blocks); err == nil && len(blocks) > 0 {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	var s string
	if err := json.Unmarshal(item.Content, &s); err == nil {
		return s
	}
	return ""
}

func fishRealtimeErrorEvent(message string) *schemas.BifrostRealtimeEvent {
	if strings.TrimSpace(message) == "" {
		message = "fish audio realtime synthesis error"
	}
	clientJSON, _ := json.Marshal(map[string]any{
		"type":  string(schemas.RTEventError),
		"error": map[string]any{"type": "fish_audio_error", "message": message},
	})
	return &schemas.BifrostRealtimeEvent{
		Type:    schemas.RTEventError,
		Error:   &schemas.RealtimeError{Type: "fish_audio_error", Message: message},
		RawData: clientJSON,
	}
}
