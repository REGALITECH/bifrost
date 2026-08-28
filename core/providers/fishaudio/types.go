package fishaudio

// This file defines ALL Fish Audio wire types.
//
// The /v1/tts/live WebSocket exchanges MessagePack-encoded events, each a map
// with an "event" discriminator (msgpack tags). The HTTP /model endpoint (used
// by ListModels) and HTTP error bodies are JSON (json tags). The two encodings
// are kept in separate structs with the appropriate struct tags.

// # SPEECH (WEBSOCKET) TYPES

// FishAudioProsody mirrors Fish Audio's prosody object. Fields are pointers so
// only explicitly-provided values are serialized.
type FishAudioProsody struct {
	Speed  *float64 `msgpack:"speed,omitempty"`
	Volume *float64 `msgpack:"volume,omitempty"`
}

// FishAudioTTSRequest is the synthesis configuration sent inside the StartEvent.
// It matches the request body of Fish Audio's HTTP TTS API. Optional fields are
// pointers and omitted when unset so Fish applies its own defaults.
type FishAudioTTSRequest struct {
	Text                      string            `msgpack:"text"`
	Format                    string            `msgpack:"format"`
	ReferenceID               *string           `msgpack:"reference_id,omitempty"`
	SampleRate                *int              `msgpack:"sample_rate,omitempty"`
	ChunkLength               *int              `msgpack:"chunk_length,omitempty"`
	MaxNewTokens              *int              `msgpack:"max_new_tokens,omitempty"`
	RepetitionPenalty         *float64          `msgpack:"repetition_penalty,omitempty"`
	MinChunkLength            *int              `msgpack:"min_chunk_length,omitempty"`
	ConditionOnPreviousChunks *bool             `msgpack:"condition_on_previous_chunks,omitempty"`
	EarlyStopThreshold        *float64          `msgpack:"early_stop_threshold,omitempty"`
	Latency                   string            `msgpack:"latency,omitempty"`
	Temperature               *float64          `msgpack:"temperature,omitempty"`
	TopP                      *float64          `msgpack:"top_p,omitempty"`
	Normalize                 *bool             `msgpack:"normalize,omitempty"`
	MP3Bitrate                *int              `msgpack:"mp3_bitrate,omitempty"`
	Prosody                   *FishAudioProsody `msgpack:"prosody,omitempty"`
}

// FishAudioStartEvent opens a synthesis cycle, carrying the TTS configuration.
type FishAudioStartEvent struct {
	Event   string              `msgpack:"event"`
	Request FishAudioTTSRequest `msgpack:"request"`
}

// FishAudioTextEvent streams a chunk of text to synthesize.
type FishAudioTextEvent struct {
	Event string `msgpack:"event"`
	Text  string `msgpack:"text"`
}

// FishAudioControlEvent is a bare event with no payload (e.g. "flush", "stop").
type FishAudioControlEvent struct {
	Event string `msgpack:"event"`
}

// FishAudioServerEvent is the union of all server-sent events. Only the fields
// relevant to the current event are populated. Unknown fields are ignored by
// the decoder, per Fish's forward-compatibility guidance.
type FishAudioServerEvent struct {
	Event   string `msgpack:"event"`
	Audio   []byte `msgpack:"audio,omitempty"`
	Reason  string `msgpack:"reason,omitempty"`
	Message string `msgpack:"message,omitempty"`
}

// # MODELS TYPES

// FishAudioModel is a single voice model entry from GET /model.
type FishAudioModel struct {
	ID        string   `json:"_id"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Languages []string `json:"languages"`
}

// FishAudioListModelsResponse is the paginated response from GET /model.
type FishAudioListModelsResponse struct {
	Total   int              `json:"total"`
	Items   []FishAudioModel `json:"items"`
	HasMore bool             `json:"has_more"`
}

// # ERROR TYPES

// FishAudioError is the error body returned by Fish Audio's HTTP endpoints.
// Fish is not fully consistent across endpoints, so both shapes are accepted.
type FishAudioError struct {
	Detail  string `json:"detail,omitempty"`
	Message string `json:"message,omitempty"`
}
