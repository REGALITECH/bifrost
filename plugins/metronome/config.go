package metronome

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

var _ schemas.ConfigMarshallerPlugin = (*Plugin)(nil)

// MarshalConfigForStorage preserves the reference, never its resolved env value.
func (p *Plugin) MarshalConfigForStorage(raw map[string]any) (map[string]any, error) {
	for name := range raw {
		if name != "api_key" && strings.EqualFold(name, "api_key") {
			return nil, fmt.Errorf("metronome api_key field name must use lowercase api_key")
		}
	}
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
		if !cfg.APIKey.IsFromEnv() || !strings.HasPrefix(cfg.APIKey.GetRawRef(), "env.") {
			return nil, fmt.Errorf("metronome api_key must be an env.VARIABLE_NAME reference")
		}
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
	for name := range out {
		if name != "api_key" && strings.EqualFold(name, "api_key") {
			out[name] = "<REDACTED>"
		}
	}
	if cfg.APIKey != nil {
		out["api_key"] = cfg.APIKey.FullyRedacted()
	}
	return out, nil
}
