package fishaudio

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/valyala/fasthttp/fasthttputil"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type listModelsTestLogger struct{}

func (listModelsTestLogger) Debug(string, ...any)                   {}
func (listModelsTestLogger) Info(string, ...any)                    {}
func (listModelsTestLogger) Warn(string, ...any)                    {}
func (listModelsTestLogger) Error(string, ...any)                   {}
func (listModelsTestLogger) Fatal(string, ...any)                   {}
func (listModelsTestLogger) SetLevel(schemas.LogLevel)              {}
func (listModelsTestLogger) SetOutputType(schemas.LoggerOutputType) {}
func (listModelsTestLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func newListModelsTestProvider(t *testing.T, handler http.HandlerFunc) *FishAudioProvider {
	t.Helper()
	// Keep real HTTP handling without requiring a TCP listener in the sandbox.
	listener := fasthttputil.NewInmemoryListener()
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	t.Cleanup(server.Close)
	provider := NewFishAudioProvider(&schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:             server.URL,
			AllowPrivateNetwork: true,
		},
	}, listModelsTestLogger{})
	provider.client.Dial = func(string) (net.Conn, error) { return listener.Dial() }
	t.Cleanup(provider.client.CloseIdleConnections)
	return provider
}

func listModelsTestKey() schemas.Key {
	return schemas.Key{
		ID:     "fish-test-key",
		Value:  *schemas.NewSecretVar("test-key"),
		Models: schemas.WhiteList{"*"},
	}
}

func listModelsTestContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
}

func assertListModelIDs(t *testing.T, response *schemas.BifrostListModelsResponse, want []string) {
	t.Helper()
	if response == nil {
		t.Fatal("response is nil")
	}
	got := make([]string, 0, len(response.Data))
	for _, model := range response.Data {
		got = append(got, model.ID)
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("model IDs = %v, want %v", got, want)
	}
}

func TestFishAudioListModelsDefault(t *testing.T) {
	for _, tc := range []struct {
		name        string
		models      schemas.WhiteList
		aliases     schemas.KeyAliases
		blacklist   schemas.BlackList
		unfiltered  bool
		extraParams map[string]interface{}
		want        []string
	}{
		{name: "wildcard", models: schemas.WhiteList{"*"}},
		{name: "empty extra params", models: schemas.WhiteList{"*"}, extraParams: map[string]interface{}{}},
		{name: "explicit models", models: schemas.WhiteList{"s1", "s2-pro"}, want: []string{"fishaudio/s1", "fishaudio/s2-pro"}},
		{name: "aliases", models: schemas.WhiteList{"*"}, aliases: schemas.KeyAliases{"tts-default": {ModelID: "s2-pro"}}, want: []string{"fishaudio/tts-default"}},
		{name: "unfiltered", models: schemas.WhiteList{"*"}, unfiltered: true},
		{name: "unfiltered skips backfill", models: schemas.WhiteList{"s1"}, aliases: schemas.KeyAliases{"tts-default": {ModelID: "s2-pro"}}, unfiltered: true},
		{name: "blacklisted models", models: schemas.WhiteList{"s1", "s2-pro"}, blacklist: schemas.BlackList{"s1"}, want: []string{"fishaudio/s2-pro"}},
		{name: "blacklisted aliases", models: schemas.WhiteList{"*"}, aliases: schemas.KeyAliases{"tts-default": {ModelID: "s2-pro"}}, blacklist: schemas.BlackList{"tts-default"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := newListModelsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/model" {
					t.Errorf("request = %s %s, want GET /model", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
					t.Errorf("authorization = %q, want Bearer test-key", got)
				}
				for param, want := range map[string]string{"self": "true", "page_size": "1", "page_number": "1"} {
					if got := r.URL.Query().Get(param); got != want {
						t.Errorf("%s = %q, want %q", param, got, want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"total":2,"items":[{"_id":"voice-one","title":"One"},{"_id":"voice-two","title":"Two"}],"has_more":false}`)
			})
			key := listModelsTestKey()
			key.Models, key.Aliases, key.BlacklistedModels = tc.models, tc.aliases, tc.blacklist
			response, err := provider.ListModels(listModelsTestContext(), []schemas.Key{key}, &schemas.BifrostListModelsRequest{
				Provider: schemas.FishAudio, Unfiltered: tc.unfiltered, ExtraParams: tc.extraParams,
			})
			if err != nil {
				t.Fatalf("ListModels failed: %v", err)
			}
			assertListModelIDs(t, response, tc.want)
			for _, model := range response.Data {
				if strings.Contains(model.ID, "voice-one") || strings.Contains(model.ID, "voice-two") {
					t.Errorf("voice ID leaked into default model list: %q", model.ID)
				}
				if model.ID == "fishaudio/tts-default" && (model.Alias == nil || *model.Alias != "s2-pro") {
					t.Errorf("alias = %v, want s2-pro", model.Alias)
				}
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestFishAudioListModelsInvalidKey(t *testing.T) {
	provider := newListModelsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"detail":"invalid api key"}`)
	})
	key := listModelsTestKey()
	_, err := provider.ListModels(listModelsTestContext(), []schemas.Key{key}, &schemas.BifrostListModelsRequest{Provider: schemas.FishAudio})
	if err == nil {
		t.Fatal("expected an error for an invalid key")
	}
	if err.StatusCode == nil || *err.StatusCode != http.StatusUnauthorized || err.Error == nil || err.Error.Message != "invalid api key" {
		t.Fatalf("unexpected Fish error: %v", err)
	}
	statuses := err.ExtraFields.KeyStatuses
	if len(statuses) != 1 {
		t.Fatalf("key statuses = %v, want one failed key", statuses)
	}
	if statuses[0].KeyID != key.ID || statuses[0].Status != schemas.KeyStatusListModelsFailed || statuses[0].Provider != schemas.FishAudio || statuses[0].Error == nil {
		t.Errorf("unexpected key status: %+v", statuses[0])
	}
}

func TestFishAudioListModelsExplicit(t *testing.T) {
	var calls atomic.Int32
	provider := newListModelsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		for param, want := range map[string]string{"self": "false", "language": "en", "page_size": "100", "page_number": strconv.Itoa(int(call))} {
			if got := r.URL.Query().Get(param); got != want {
				t.Errorf("%s = %q, want %q", param, got, want)
			}
		}
		page := FishAudioListModelsResponse{Total: fishModelsPageSize + 1}
		if call == 1 {
			page.HasMore = true
			for i := 0; i < fishModelsPageSize; i++ {
				page.Items = append(page.Items, FishAudioModel{ID: fmt.Sprintf("voice-%d", i)})
			}
		} else {
			page.Items = []FishAudioModel{{ID: "voice-last"}}
		}
		body, err := providerUtils.MarshalSorted(page)
		if err != nil {
			t.Errorf("marshal page: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	response, err := provider.ListModels(listModelsTestContext(), []schemas.Key{listModelsTestKey()}, &schemas.BifrostListModelsRequest{
		Provider: schemas.FishAudio, ExtraParams: map[string]interface{}{"self": "false", "language": "en"},
	})
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	want := []string{"fishaudio/voice-last"}
	for i := 0; i < fishModelsPageSize; i++ {
		want = append(want, fmt.Sprintf("fishaudio/voice-%d", i))
	}
	assertListModelIDs(t, response, want)
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestFishAudioListModelsValidationStopsAfterOnePage(t *testing.T) {
	var calls atomic.Int32
	provider := newListModelsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// A full page with has_more=true must still cause only one validation call.
		items := make([]FishAudioModel, fishModelsPageSize)
		for i := range items {
			items[i].ID = fmt.Sprintf("voice-%d", i)
		}
		body, err := providerUtils.MarshalSorted(FishAudioListModelsResponse{Items: items, HasMore: true})
		if err != nil {
			t.Errorf("marshal page: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Request-ID", "validation-call")
		w.Write(body)
	})
	response, err := provider.listModelsByKey(listModelsTestContext(), listModelsTestKey(), &schemas.BifrostListModelsRequest{})
	if err != nil {
		t.Fatalf("listModelsByKey failed: %v", err)
	}
	if len(response.Data) != 0 {
		t.Errorf("models = %d, want 0", len(response.Data))
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
	if response.ExtraFields.Latency < 0 {
		t.Errorf("latency = %d, want non-negative validation call latency", response.ExtraFields.Latency)
	}
	if got := response.ExtraFields.ProviderResponseHeaders["X-Request-Id"]; got != "validation-call" {
		t.Errorf("provider response header = %q, want validation-call", got)
	}
}
