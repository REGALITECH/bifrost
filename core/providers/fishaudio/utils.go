package fishaudio

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ws "github.com/fasthttp/websocket"
	"github.com/vmihailenco/msgpack/v5"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
)

// # BASE URLs / ENDPOINTS

const (
	fishDefaultBaseURL = "https://api.fish.audio"
	fishTTSLivePath    = "/v1/tts/live"
	fishModelsPath     = "/model"
)

// # DEFAULTS AND LIMITS

const (
	fishDefaultModel       = "s2-pro"
	fishModelsPageSize     = 100
	fishDefaultTimeoutSecs = 30
)

// # PROVIDER ENUMS / CONSTANTS

// Fish Audio WebSocket event names.
const (
	fishEventStart  = "start"
	fishEventText   = "text"
	fishEventFlush  = "flush"
	fishEventStop   = "stop"
	fishEventAudio  = "audio"
	fishEventFinish = "finish"
	fishEventError  = "error"

	fishFinishReasonStop  = "stop"
	fishFinishReasonError = "error"
)

// supportedFishFormats are the output audio formats Fish Audio accepts.
var supportedFishFormats = map[string]struct{}{
	"mp3":  {},
	"pcm":  {},
	"wav":  {},
	"opus": {},
}

// # WEBSOCKET HELPERS

// fishWebSocketURL converts the configured HTTP base URL into the wss tts-live
// endpoint (https://api.fish.audio -> wss://api.fish.audio/v1/tts/live).
func fishWebSocketURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return base + fishTTSLivePath
}

// buildWSHeaders builds the auth + model headers for the tts-live handshake.
// "model" is set via direct map assignment to preserve the lowercase casing
// Fish expects (http.Header.Set would canonicalize it to "Model").
func (provider *FishAudioProvider) buildWSHeaders(key schemas.Key, model string) http.Header {
	headers := http.Header{}
	if value := key.Value.GetValue(); value != "" {
		headers.Set("Authorization", "Bearer "+value)
	}
	if model != "" {
		headers["model"] = []string{model}
	}
	for k, v := range provider.networkConfig.ExtraHeaders {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "model") {
			continue
		}
		headers.Set(k, v)
	}
	return headers
}

// dial opens the tts-live WebSocket, mapping handshake failures to the same
// error shapes the rest of the provider uses (the upstream status code is
// preserved, so a 429 maps to RESOURCE_EXHAUSTED downstream).
func (provider *FishAudioProvider) dial(url string, headers http.Header) (*ws.Conn, *schemas.BifrostError) {
	dialer := ws.Dialer{HandshakeTimeout: provider.requestTimeout()}

	conn, resp, err := dialer.Dial(url, headers)
	if err != nil {
		if resp != nil {
			status := resp.StatusCode
			body := readErrorBody(resp)
			return nil, &schemas.BifrostError{
				IsBifrostError: false,
				StatusCode:     schemas.Ptr(status),
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr("fish_audio_handshake_error"),
					Message: fmt.Sprintf("fish audio websocket handshake failed (status %d): %s", status, body),
				},
			}
		}
		return nil, providerUtils.NewBifrostOperationError("failed to open fish audio websocket", err)
	}
	return conn, nil
}

// runSynthesis sends Start+Text+Stop on an already-open connection and invokes
// onAudio for every audio frame until Fish replies finish(stop). It does NOT
// open or close the connection — the caller owns its lifecycle. readIdleTimeout
// bounds the wait for each frame (0 disables it).
func runSynthesis(
	conn *ws.Conn,
	request *FishAudioTTSRequest,
	text string,
	writeTimeout time.Duration,
	readIdleTimeout time.Duration,
	onAudio func([]byte) *schemas.BifrostError,
) *schemas.BifrostError {
	if err := writeMsgpack(conn, FishAudioStartEvent{Event: fishEventStart, Request: *request}, writeTimeout); err != nil {
		return providerUtils.NewBifrostOperationError("failed to send fish audio start event", err)
	}
	if err := writeMsgpack(conn, FishAudioTextEvent{Event: fishEventText, Text: text}, writeTimeout); err != nil {
		return providerUtils.NewBifrostOperationError("failed to send fish audio text event", err)
	}
	if err := writeMsgpack(conn, FishAudioControlEvent{Event: fishEventStop}, writeTimeout); err != nil {
		return providerUtils.NewBifrostOperationError("failed to send fish audio stop event", err)
	}

	for {
		if readIdleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(readIdleTimeout))
		}
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return providerUtils.NewBifrostOperationError("fish audio websocket read failed", err)
		}
		// Fish sends only binary msgpack frames; ignore anything else.
		if msgType != ws.BinaryMessage {
			continue
		}

		var event FishAudioServerEvent
		if err := msgpack.Unmarshal(data, &event); err != nil {
			return providerUtils.NewBifrostOperationError("failed to decode fish audio event", err)
		}

		switch event.Event {
		case fishEventAudio:
			if len(event.Audio) > 0 {
				if bifrostErr := onAudio(event.Audio); bifrostErr != nil {
					return bifrostErr
				}
			}
		case fishEventError:
			return fishServerError(event.Message)
		case fishEventFinish:
			if event.Reason == fishFinishReasonError {
				message := event.Message
				if strings.TrimSpace(message) == "" {
					// Fish often closes with finish(error) and no detail; the
					// usual culprits are an unknown voice reference_id or an
					// invalid model variant, so point the caller there.
					message = "fish audio synthesis failed (finish reason=error) — check the voice reference_id and model variant (s1 / s2-pro)"
				}
				return fishServerError(message)
			}
			return nil
		default:
			// Ignore unknown events for forward compatibility.
		}
	}
}

// writeMsgpack encodes v as MessagePack and writes it as a binary frame.
func writeMsgpack(conn *ws.Conn, v any, timeout time.Duration) error {
	payload, err := msgpack.Marshal(v)
	if err != nil {
		return err
	}
	if timeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	}
	return conn.WriteMessage(ws.BinaryMessage, payload)
}

// # ERROR HELPERS

// fishServerError builds a BifrostError from a server-reported synthesis error,
// flagging rate-limit messages with a 429 so they map to RESOURCE_EXHAUSTED.
func fishServerError(message string) *schemas.BifrostError {
	if strings.TrimSpace(message) == "" {
		message = "fish audio synthesis error"
	}
	statusCode := 502
	if isRateLimitMessage(message) {
		statusCode = 429
	}
	return &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     schemas.Ptr(statusCode),
		Error: &schemas.ErrorField{
			Type:    schemas.Ptr("fish_audio_error"),
			Message: message,
		},
	}
}

func isRateLimitMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "429")
}

// readErrorBody reads (and bounds) a failed-handshake HTTP response body.
func readErrorBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// # SPEECH REQUEST HELPERS

// fishVoiceFromRequest extracts the single-voice reference_id from the request.
//
// ListModels returns IDs namespaced as "fishaudio/<reference_id>", so callers
// commonly paste the full ID into the voice field. Fish's reference_id is the
// bare value, so a leading "fishaudio/" prefix is stripped defensively.
func fishVoiceFromRequest(request *schemas.BifrostSpeechRequest) string {
	if request.Params == nil || request.Params.VoiceConfig == nil {
		return ""
	}
	if voice := request.Params.VoiceConfig.Voice; voice != nil {
		return strings.TrimPrefix(strings.TrimSpace(*voice), string(schemas.FishAudio)+"/")
	}
	return ""
}

// normalizeFishFormat returns the Fish-compatible format, or "" to fall back
// to the default (mp3).
func normalizeFishFormat(format string) string {
	normalized := strings.ToLower(strings.TrimSpace(format))
	// OpenAI Realtime clients call PCM "pcm16", while Fish accepts "pcm".
	if normalized == "pcm16" {
		return "pcm"
	}
	if _, ok := supportedFishFormats[normalized]; ok {
		return normalized
	}
	return ""
}
