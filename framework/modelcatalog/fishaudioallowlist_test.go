// REGALITECH/etla#256 relies on provider-key models registering Fish Audio TTS
// models for wildcard virtual keys. New models need only a key configuration
// update; a wildcard provider key alone does not register unknown models.
package modelcatalog

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
)

func TestFishAudioAllowlist_RegisteredKeyModelsPassWildcardVirtualKey(t *testing.T) {
	mc := NewTestCatalog(nil)
	key := schemas.Key{ID: "fish-key", Models: schemas.WhiteList{"s1", "s2-pro", "s2.1-pro"}}
	mc.SetKeyConfigForProvider(schemas.FishAudio, []schemas.Key{key})
	mc.UpsertLive(schemas.FishAudio, key.ID, false, []string{"s1", "s2-pro", "s2.1-pro"})
	allowedModels := schemas.WhiteList{"*"}

	for _, model := range []string{"s1", "s2-pro", "s2.1-pro"} {
		if !mc.IsModelAllowedForProvider(schemas.FishAudio, model, nil, allowedModels) {
			t.Errorf("wildcard virtual key denied registered model %q", model)
		}
	}
	if mc.IsModelAllowedForProvider(schemas.FishAudio, "s3", nil, allowedModels) {
		t.Fatal("wildcard virtual key allowed s3 before registration")
	}

	// Register s3 through key configuration alone, leaving the live list unchanged.
	key.Models = schemas.WhiteList{"s1", "s2-pro", "s2.1-pro", "s3"}
	mc.SetKeyConfigForProvider(schemas.FishAudio, []schemas.Key{key})
	if !mc.IsModelAllowedForProvider(schemas.FishAudio, "s3", nil, allowedModels) {
		t.Error("wildcard virtual key denied s3 after registration in provider-key models")
	}
}

func TestFishAudioAllowlist_WildcardKeyUsesDerivedDatasheetModels(t *testing.T) {
	pricingPath := filepath.Join(t.TempDir(), "pricing.json")
	pricingJSON := []byte(`{
		"openrouter/fish-audio/s1": {"provider":"openrouter","mode":"chat","input_cost_per_token":0.000015},
		"openrouter/fish-audio/s2-pro": {"provider":"openrouter","mode":"chat","input_cost_per_token":0.000015},
		"openrouter/fish-audio/s2.1-pro": {"provider":"openrouter","mode":"chat","input_cost_per_token":0.000015}
	}`)
	if err := os.WriteFile(pricingPath, pricingJSON, 0o600); err != nil {
		t.Fatalf("write pricing testdata: %v", err)
	}
	ds := datasheet.New(nil, nil, datasheet.Config{URL: "file://" + pricingPath})
	if err := ds.LoadFromURLIntoMemory(t.Context()); err != nil {
		t.Fatalf("load pricing testdata: %v", err)
	}

	mc := NewTestCatalogWithDatasheet(ds)
	key := schemas.Key{ID: "fish-key", Models: schemas.WhiteList{"*"}}
	mc.SetKeyConfigForProvider(schemas.FishAudio, []schemas.Key{key})
	mc.UpsertLive(schemas.FishAudio, key.ID, false, nil)
	allowedModels := schemas.WhiteList{"*"}

	if models := mc.GetModelsForProvider(schemas.FishAudio); !slices.Contains(models, "s2-pro") {
		t.Fatalf("Fish Audio catalog = %v, want derived s2-pro with an empty live list", models)
	}
	if !mc.IsModelAllowedForProvider(schemas.FishAudio, "s2-pro", nil, allowedModels) {
		t.Error("wildcard virtual key denied datasheet-derived s2-pro")
	}
	if mc.IsModelAllowedForProvider(schemas.FishAudio, "s3", nil, allowedModels) {
		t.Fatal("wildcard provider key allowed unknown s3 through wildcard virtual key")
	}

	// Replace the wildcard with explicit models to register s3 without live data.
	key.Models = schemas.WhiteList{"s1", "s2-pro", "s2.1-pro", "s3"}
	mc.SetKeyConfigForProvider(schemas.FishAudio, []schemas.Key{key})
	if !mc.IsModelAllowedForProvider(schemas.FishAudio, "s3", nil, allowedModels) {
		t.Error("wildcard virtual key denied s3 after replacing wildcard provider-key models with an explicit list")
	}
}
