package fishaudio

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("ListModels unexpectedly called Fish: %s %s", r.Method, r.URL)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"total":1,"items":[{"_id":"voice-id","title":"Voice"}],"has_more":false}`)
	}))
	t.Cleanup(server.Close)
	provider := NewFishAudioProvider(&schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{BaseURL: server.URL, AllowPrivateNetwork: true},
	}, nil)
	allModels := []string{"fishaudio/s1", "fishaudio/s2-pro", "fishaudio/s2.1-pro", "fishaudio/s2.1-pro-free"}
	for _, tc := range []struct {
		name       string
		models     schemas.WhiteList
		blacklist  schemas.BlackList
		unfiltered bool
		want       []string
	}{
		{name: "wildcard key lists every TTS model", models: schemas.WhiteList{"*"}, want: allModels},
		{name: "explicit key lists only its models", models: schemas.WhiteList{"s1", "s2-pro"}, want: []string{"fishaudio/s1", "fishaudio/s2-pro"}},
		{name: "blacklist excludes a TTS model", models: schemas.WhiteList{"*"}, blacklist: schemas.BlackList{"s1"}, want: allModels[1:]},
		{name: "unfiltered ignores allowlist and blacklist", models: schemas.WhiteList{"s1"}, blacklist: schemas.BlackList{"s2-pro"}, unfiltered: true, want: allModels},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
			t.Cleanup(ctx.Cancel)
			resp, err := provider.ListModels(ctx, []schemas.Key{{
				ID:                "fish-key",
				Models:            tc.models,
				BlacklistedModels: tc.blacklist,
			}}, &schemas.BifrostListModelsRequest{
				Provider:   schemas.FishAudio,
				Unfiltered: tc.unfiltered,
			})
			if err != nil {
				t.Fatalf("ListModels failed: %v", err)
			}
			if resp == nil {
				t.Fatal("ListModels returned no response")
			}
			got := make([]string, 0, len(resp.Data))
			for _, model := range resp.Data {
				got = append(got, model.ID)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("model IDs = %v, want %v", got, tc.want)
			}
			if len(resp.KeyStatuses) != 1 || resp.KeyStatuses[0].KeyID != "fish-key" || resp.KeyStatuses[0].Status != schemas.KeyStatusSuccess {
				t.Errorf("expected a successful status for fish-key, got %+v", resp.KeyStatuses)
			}
		})
	}
}
