package metronome

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

var _ schemas.ConfigMarshallerPlugin = (*Plugin)(nil)

var envReference = regexp.MustCompile(`^env\.[A-Za-z_][A-Za-z0-9_]*$`)

// apiKeyReference only parses references. In particular, it never resolves a
// vault reference or trusts a client-supplied resolved SecretVar value.
func apiKeyReference(data json.RawMessage) (string, error) {
	if len(data) == 0 || string(data) == "null" {
		return "", nil
	}
	var ref string
	if err := json.Unmarshal(data, &ref); err != nil {
		var key struct {
			Ref     string `json:"ref"`
			EnvVar  string `json:"env_var"`
			FromEnv bool   `json:"from_env"`
		}
		if json.Unmarshal(data, &key) != nil {
			return "", fmt.Errorf("metronome api_key must be an env.VARIABLE_NAME reference")
		}
		ref = key.Ref
		if ref == "" && key.FromEnv {
			ref = key.EnvVar
			if !strings.HasPrefix(ref, "env.") {
				ref = "env." + ref
			}
		}
		if ref == "" {
			return "", fmt.Errorf("metronome api_key must be an env.VARIABLE_NAME reference")
		}
	}
	if ref != "" && !envReference.MatchString(ref) {
		return "", fmt.Errorf("metronome api_key must be an env.VARIABLE_NAME reference")
	}
	return ref, nil
}

// Only genuinely empty legacy fields can be removed automatically. Malformed
// or populated values stay visible until an operator migrates the ingest aliases.
func emptyLegacyField(name string, data json.RawMessage) bool {
	if len(data) == 0 || string(data) == "null" {
		return true
	}
	if name == "customer_mapping" {
		var value map[string]string
		return json.Unmarshal(data, &value) == nil && len(value) == 0
	}
	var value string
	return json.Unmarshal(data, &value) == nil && value == ""
}

// MarshalConfigForStorage preserves the reference, never its resolved env value.
func (p *Plugin) MarshalConfigForStorage(raw map[string]any) (map[string]any, error) {
	// encoding/json accepts case-insensitive struct field names at runtime.
	// Reject aliases here so a differently cased key cannot bypass validation
	// or survive alongside the canonical api_key in the stored raw map.
	for name := range raw {
		if name != "api_key" && strings.EqualFold(name, "api_key") {
			return nil, fmt.Errorf("metronome api_key field name must use lowercase api_key")
		}
	}
	data, err := schemas.MarshalSorted(raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	ref, err := apiKeyReference(fields["api_key"])
	if err != nil {
		return nil, err
	}
	out := maps.Clone(raw)
	if out == nil {
		out = make(map[string]any)
	}
	if _, exists := fields["api_key"]; exists {
		out["api_key"] = ref
	}
	for _, name := range []string{"customer_mapping", "default_customer_id"} {
		if emptyLegacyField(name, fields[name]) {
			delete(out, name)
		}
	}
	return out, nil
}

// RedactConfig follows the builtin SecretVar API contract.
func (p *Plugin) RedactConfig(raw map[string]any) (map[string]any, error) {
	data, err := schemas.MarshalSorted(raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	out := maps.Clone(raw)
	for name, key := range fields {
		if !strings.EqualFold(name, "api_key") {
			continue
		}
		ref, err := apiKeyReference(key)
		if err != nil {
			// Legacy literal credentials must never leak, even on a disabled or
			// failed plugin. The operator must replace this with an env reference.
			out[name] = "<REDACTED>"
		} else {
			out[name] = ref
		}
	}
	return out, nil
}
