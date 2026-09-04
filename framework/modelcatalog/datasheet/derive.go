// This file is a REGALITECH fork divergence. OpenRouter resells Fish Audio at
// list price per UTF-8 byte, while this fork's Fish Audio provider reports
// billable bytes as input_chars and prices them through input_cost_per_character.
// The upstream datasheet does not yet publish native `fishaudio/*` catalog
// rows, so they are derived from the corresponding OpenRouter rows during every
// load.
package datasheet

import "github.com/maximhq/bifrost/core/schemas"

// derivation mints catalog rows for a provider whose prices the datasheet
// publishes only under another provider's key namespace.
type derivation struct {
	sourcePrefix   string   // datasheet key prefix, e.g. "openrouter/fish-audio/"
	sourceProvider string   // provider the source row must declare, e.g. "openrouter"
	sourceMode     string   // mode the source row must declare, e.g. "chat"
	targetProvider string   // provider of the minted row, e.g. "fishaudio"
	targetMode     string   // mode of the minted row, e.g. "audio_speech"
	models         []string // model names to derive; anything else under the prefix is ignored
}

// builtinDerivations lists catalog rows that Bifrost derives from upstream
// datasheet entries during every pricing load.
var builtinDerivations = []derivation{
	{
		sourcePrefix:   "openrouter/fish-audio/",
		sourceProvider: "openrouter",
		sourceMode:     "chat",
		targetProvider: "fishaudio",
		targetMode:     "audio_speech",
		models:         []string{"s1", "s2-pro", "s2.1-pro"},
	},
}

// applyDerivations adds valid derived pricing entries without replacing native
// target-provider rows and returns the number of entries it added.
func applyDerivations(pricing map[string]Entry, rules []derivation, logger schemas.Logger) int {
	derived := 0
	for _, rule := range rules {
		for _, model := range rule.models {
			sourceKey := rule.sourcePrefix + model
			source, ok := pricing[sourceKey]
			if !ok {
				if logger != nil {
					logger.Debug("skipping pricing derivation for %s: source row %s is absent", model, sourceKey)
				}
				continue
			}
			if source.Provider != rule.sourceProvider {
				if logger != nil {
					logger.Debug("skipping pricing derivation for %s: source provider %q does not match %q", model, source.Provider, rule.sourceProvider)
				}
				continue
			}
			if source.Mode != rule.sourceMode {
				if logger != nil {
					logger.Debug("skipping pricing derivation for %s: source mode %q does not match %q", model, source.Mode, rule.sourceMode)
				}
				continue
			}
			if source.InputCostPerToken == nil || *source.InputCostPerToken <= 0 {
				if logger != nil {
					logger.Debug("skipping pricing derivation for %s: source input_cost_per_token is missing or non-positive", model)
				}
				continue
			}

			targetKey := rule.targetProvider + "/" + model
			if _, ok := pricing[targetKey]; ok {
				if logger != nil {
					logger.Debug("skipping pricing derivation for %s: native target row %s already exists", model, targetKey)
				}
				continue
			}

			inputCostPerCharacter := *source.InputCostPerToken
			pricing[targetKey] = Entry{
				Provider:  rule.targetProvider,
				Mode:      rule.targetMode,
				BaseModel: model,
				Options: Options{
					InputCostPerCharacter: &inputCostPerCharacter,
				},
			}
			derived++
		}
	}

	if derived > 0 && logger != nil {
		logger.Info("derived %d pricing records from upstream datasheet entries", derived)
	}
	return derived
}
