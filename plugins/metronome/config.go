package metronome

import (
	"encoding/json"
	"maps"

	"github.com/maximhq/bifrost/core/schemas"
)

var _ schemas.ConfigMarshallerPlugin = (*Plugin)(nil)

// MarshalConfigForStorage preserves the reference, never its resolved env value.
func (p *Plugin) MarshalConfigForStorage(raw map[string]any) (map[string]any, error) {
	data, err := schemas.MarshalSorted(raw)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	out := maps.Clone(raw)
	if out == nil {
		out = make(map[string]any)
	}
	if cfg.APIKey != nil {
		out["api_key"] = schemas.SecretVarAsString(cfg.APIKey)
	}
	return out, nil
}

// RedactConfig follows the builtin SecretVar API contract.
func (p *Plugin) RedactConfig(raw map[string]any) (map[string]any, error) {
	data, err := schemas.MarshalSorted(raw)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	out := maps.Clone(raw)
	if cfg.APIKey != nil {
		out["api_key"] = cfg.APIKey.FullyRedacted()
	}
	return out, nil
}
