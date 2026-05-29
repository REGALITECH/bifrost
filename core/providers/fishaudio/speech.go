package fishaudio

import (
	"github.com/maximhq/bifrost/core/schemas"
)

// ToFishAudioSpeechRequest maps a Bifrost speech request to Fish's TTS
// configuration.
//
// The configuration is sent inside the StartEvent; the text itself is streamed
// separately via a TextEvent, so Request.Text is intentionally left empty here.
// Provider-specific knobs (temperature, top_p, normalize, sample_rate,
// chunk_length, mp3_bitrate, latency, volume) are read from ExtraParams.
func ToFishAudioSpeechRequest(bifrostReq *schemas.BifrostSpeechRequest) *FishAudioTTSRequest {
	fishReq := &FishAudioTTSRequest{
		Format: "mp3",
	}

	if bifrostReq == nil || bifrostReq.Params == nil {
		return fishReq
	}
	params := bifrostReq.Params

	if voice := fishVoiceFromRequest(bifrostReq); voice != "" {
		fishReq.ReferenceID = schemas.Ptr(voice)
	}

	if format := normalizeFishFormat(params.ResponseFormat); format != "" {
		fishReq.Format = format
	}

	prosody := FishAudioProsody{}
	hasProsody := false
	if params.Speed != nil {
		prosody.Speed = params.Speed
		hasProsody = true
	}

	if params.ExtraParams != nil {
		if temperature, ok := schemas.SafeExtractFloat64Pointer(params.ExtraParams["temperature"]); ok {
			fishReq.Temperature = temperature
		}
		if topP, ok := schemas.SafeExtractFloat64Pointer(params.ExtraParams["top_p"]); ok {
			fishReq.TopP = topP
		}
		if normalize, ok := schemas.SafeExtractBoolPointer(params.ExtraParams["normalize"]); ok {
			fishReq.Normalize = normalize
		}
		if sampleRate, ok := schemas.SafeExtractIntPointer(params.ExtraParams["sample_rate"]); ok {
			fishReq.SampleRate = sampleRate
		}
		if chunkLength, ok := schemas.SafeExtractIntPointer(params.ExtraParams["chunk_length"]); ok {
			fishReq.ChunkLength = chunkLength
		}
		if mp3Bitrate, ok := schemas.SafeExtractIntPointer(params.ExtraParams["mp3_bitrate"]); ok {
			fishReq.MP3Bitrate = mp3Bitrate
		}
		if latency, ok := schemas.SafeExtractStringPointer(params.ExtraParams["latency"]); ok {
			fishReq.Latency = *latency
		}
		if volume, ok := schemas.SafeExtractFloat64Pointer(params.ExtraParams["volume"]); ok {
			prosody.Volume = volume
			hasProsody = true
		}
	}

	if hasProsody {
		fishReq.Prosody = &prosody
	}

	return fishReq
}
