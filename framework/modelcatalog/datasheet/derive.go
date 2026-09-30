// This file is a REGALITECH fork divergence. OpenRouter resells Fish Audio at
// list price per UTF-8 byte, while this fork's Fish Audio provider reports
// billable bytes as input_chars and prices them through input_cost_per_character.
// The upstream datasheet does not yet publish native `fishaudio/*` catalog
// rows, so they are derived from the corresponding OpenRouter rows during every
// load.
package datasheet

import "github.com/maximhq/bifrost/core/schemas"

// fishAudioPricingModels lists TTS models whose pricing is derived from OpenRouter.
var fishAudioPricingModels = []string{"s1", "s2-pro", "s2.1-pro"}

// deriveFishAudioPricing adds Fish Audio pricing from OpenRouter rows without
// replacing native Fish Audio rows and returns the number of entries it added.
func deriveFishAudioPricing(pricing map[string]Entry, logger schemas.Logger) int {
	derived := 0
	for _, model := range fishAudioPricingModels {
		sourceKey := "openrouter/fish-audio/" + model
		source, ok := pricing[sourceKey]
		if !ok {
			if logger != nil {
				logger.Debug("skipping pricing derivation for %s: source row %s is absent", model, sourceKey)
			}
			continue
		}
		if source.Provider != string(schemas.OpenRouter) {
			if logger != nil {
				logger.Debug("skipping pricing derivation for %s: source provider %q does not match %q", model, source.Provider, string(schemas.OpenRouter))
			}
			continue
		}
		if source.Mode != "chat" {
			if logger != nil {
				logger.Debug("skipping pricing derivation for %s: source mode %q does not match %q", model, source.Mode, "chat")
			}
			continue
		}
		if source.InputCostPerToken == nil || *source.InputCostPerToken <= 0 {
			if logger != nil {
				logger.Debug("skipping pricing derivation for %s: source input_cost_per_token is missing or non-positive", model)
			}
			continue
		}

		targetKey := string(schemas.FishAudio) + "/" + model
		if _, ok := pricing[targetKey]; ok {
			if logger != nil {
				logger.Debug("skipping pricing derivation for %s: native target row %s already exists", model, targetKey)
			}
			continue
		}

		inputCostPerCharacter := *source.InputCostPerToken
		pricing[targetKey] = Entry{
			Provider:  string(schemas.FishAudio),
			Mode:      "audio_speech",
			BaseModel: model,
			Options: Options{
				InputCostPerCharacter: &inputCostPerCharacter,
			},
		}
		derived++
	}

	if derived > 0 && logger != nil {
		logger.Info("derived %d pricing records from upstream datasheet entries", derived)
	}
	return derived
}
