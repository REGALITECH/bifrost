// REGALITECH/etla#256: a virtual key with fishaudio allowed_models ["*"] must
// accept the Fish Audio TTS models. Fish has no TTS model-list API, so the
// provider's ListModels returns the fixed schemas.FishAudioTTSModels list. These
// tests feed that list into the live catalog the way the server's
// FetchAndStoreLiveForKey does, then run the wildcard virtual-key check.
package modelcatalog

import (
	"testing"

	"github.com/maximhq/bifrost/core/providers/fishaudio"
	"github.com/maximhq/bifrost/core/schemas"
)

// refreshFishLiveModels stores the Fish Audio provider's filtered and
// unfiltered model lists per key, mirroring FetchAndStoreLiveForKey.
func refreshFishLiveModels(mc *ModelCatalog, keys []schemas.Key) {
	mc.SetKeyConfigForProvider(schemas.FishAudio, keys)
	for _, key := range keys {
		for _, unfiltered := range []bool{false, true} {
			resp := fishaudio.ToBifrostListModelsResponse(schemas.FishAudio, key.Models, key.BlacklistedModels, key.Aliases, unfiltered)
			mc.UpsertLiveFromResponse(schemas.FishAudio, key.ID, unfiltered, resp)
		}
	}
}

func TestFishAudioAllowlist_WildcardVirtualKey(t *testing.T) {
	wildcard := schemas.WhiteList{"*"}
	for _, tc := range []struct {
		name    string
		keys    []schemas.Key
		allowed []string
		denied  []string
	}{
		{
			name:    "wildcard key",
			keys:    []schemas.Key{{ID: "fish-key", Models: wildcard}},
			allowed: []string{"s1", "s2-pro", "s2.1-pro", "s2.1-pro-free"},
			denied:  []string{"s9", "drama-3", "11a08586fe57442a95ef3f81160cc583"},
		},
		{
			name:    "wildcard key with an alias",
			keys:    []schemas.Key{{ID: "fish-key", Models: wildcard, Aliases: schemas.KeyAliases{"tts-default": {ModelID: "s2-pro"}}}},
			allowed: []string{"s1", "s2-pro", "s2.1-pro", "s2.1-pro-free", "tts-default"},
			denied:  []string{"s9"},
		},
		{
			name: "wildcard key next to an explicit key",
			keys: []schemas.Key{
				{ID: "wildcard-key", Models: wildcard},
				{ID: "explicit-key", Models: schemas.WhiteList{"s2-pro"}},
			},
			allowed: []string{"s1", "s2-pro", "s2.1-pro", "s2.1-pro-free"},
			denied:  []string{"s9"},
		},
		{
			name:    "explicit key registers a model outside the fixed list",
			keys:    []schemas.Key{{ID: "fish-key", Models: schemas.WhiteList{"s2.1-pro", "s3"}}},
			allowed: []string{"s2.1-pro", "s3"},
			denied:  []string{"s1", "s9"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No datasheet rows for fishaudio: the result must not depend on pricing data.
			mc := NewTestCatalog(nil)
			refreshFishLiveModels(mc, tc.keys)
			for _, model := range tc.allowed {
				if !mc.IsModelAllowedForProvider(schemas.FishAudio, model, nil, wildcard) {
					t.Errorf("wildcard virtual key denied %q", model)
				}
			}
			for _, model := range tc.denied {
				if mc.IsModelAllowedForProvider(schemas.FishAudio, model, nil, wildcard) {
					t.Errorf("wildcard virtual key allowed %q", model)
				}
			}
		})
	}
}
