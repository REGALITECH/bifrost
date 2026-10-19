package schemas

// FishAudioTTSModels returns the Fish Audio TTS model variants Bifrost serves,
// as sent in Fish's `model` header.
//
// Fish has no endpoint that lists these variants: GET /model lists voices,
// which are passed as reference_id, not as models. The Fish Audio provider's
// ListModels therefore returns this fixed list, and the datasheet derives
// pricing for the same names. Keep it in sync with the `model` enum of
// POST /v1/tts in https://docs.fish.audio/api-reference/openapi.json.
func FishAudioTTSModels() []string {
	return []string{"s1", "s2-pro", "s2.1-pro", "s2.1-pro-free"}
}
