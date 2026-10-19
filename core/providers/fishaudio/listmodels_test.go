package fishaudio

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestToBifrostListModelsResponse(t *testing.T) {
	allModels := []string{"fishaudio/s1", "fishaudio/s2-pro", "fishaudio/s2.1-pro", "fishaudio/s2.1-pro-free"}
	for _, tc := range []struct {
		name       string
		models     schemas.WhiteList
		unfiltered bool
		want       []string
	}{
		{name: "wildcard key lists every TTS model", models: schemas.WhiteList{"*"}, want: allModels},
		{name: "explicit key lists only its models", models: schemas.WhiteList{"s1", "s2-pro"}, want: []string{"fishaudio/s1", "fishaudio/s2-pro"}},
		{name: "unfiltered ignores the key", models: schemas.WhiteList{"s1"}, unfiltered: true, want: allModels},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := ToBifrostListModelsResponse(schemas.FishAudio, tc.models, nil, nil, tc.unfiltered)
			got := make([]string, 0, len(resp.Data))
			for _, model := range resp.Data {
				got = append(got, model.ID)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("model IDs = %v, want %v", got, tc.want)
			}
		})
	}
}
