package handlers

// transcriptionUsageModels is the closed set of ASR model names accepted by
// transcription usage reporting. Names match etla_streaming's normalized STT
// provider names. Add billable models only alongside their pricing entries;
// unknown represents explicitly zero-priced development usage.
var transcriptionUsageModels = map[string]struct{}{
	"qwen3-asr":          {},
	"reazonspeech-nemo":  {},
	"kotoba-whisper":     {},
	"sherpa-onnx-ja":     {},
	"sherpa-parakeet-ja": {},
	"hiragana-wav2vec2":  {},
	"unknown":            {},
}
