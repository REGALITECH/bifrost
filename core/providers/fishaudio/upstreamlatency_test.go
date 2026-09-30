package fishaudio_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ws "github.com/fasthttp/websocket"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/vmihailenco/msgpack/v5"
)

// fishSynthesisDelay is how long the fake tts-live server takes to synthesize.
const fishSynthesisDelay = 200 * time.Millisecond

// fishLatencyTestAccount exposes Fish Audio with one key, pointed at baseURL.
type fishLatencyTestAccount struct{ baseURL string }

func (fishLatencyTestAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{schemas.FishAudio}, nil
}

func (fishLatencyTestAccount) GetKeysForProvider(_ context.Context, p schemas.ModelProvider) ([]schemas.Key, error) {
	if p != schemas.FishAudio {
		return nil, nil
	}
	return []schemas.Key{{ID: "fish-key-1", Value: *schemas.NewSecretVar("fish-test-key"), Models: schemas.WhiteList{"*"}, Weight: 1.0}}, nil
}

func (a fishLatencyTestAccount) GetConfigForProvider(p schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	if p != schemas.FishAudio {
		return nil, fmt.Errorf("unsupported provider %s", p)
	}
	networkConfig := schemas.DefaultNetworkConfig
	networkConfig.BaseURL = a.baseURL
	return &schemas.ProviderConfig{
		NetworkConfig:            networkConfig,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}, nil
}

// startSlowFishTTSLive runs a fake tts-live server that waits
// fishSynthesisDelay after the stop event, then sends one audio frame and
// finish(stop).
func startSlowFishTTSLive(t *testing.T) string {
	t.Helper()
	upgrader := ws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var event map[string]any
			if err := msgpack.Unmarshal(data, &event); err != nil || event["event"] != "stop" {
				continue
			}
			time.Sleep(fishSynthesisDelay)
			for _, frame := range []map[string]any{
				{"event": "audio", "audio": []byte{1, 2, 3, 4}},
				{"event": "finish", "reason": "stop"},
			} {
				payload, _ := msgpack.Marshal(frame)
				_ = conn.WriteMessage(ws.BinaryMessage, payload)
			}
			return
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newFishLatencyTestClient(t *testing.T) *bifrost.Bifrost {
	t.Helper()
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: fishLatencyTestAccount{baseURL: startSlowFishTTSLive(t)},
		Logger:  bifrost.NewDefaultLogger(schemas.LogLevelError),
	})
	if err != nil {
		t.Fatalf("bifrost.Init: %v", err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func fishLatencyTestRequest() *schemas.BifrostSpeechRequest {
	return &schemas.BifrostSpeechRequest{Provider: schemas.FishAudio, Model: "s2-pro", Input: &schemas.SpeechInput{Input: "hello"}}
}

// assertSynthesisIsUpstream checks that the time Fish spent synthesizing is
// reported as upstream latency, not as Bifrost overhead.
func assertSynthesisIsUpstream(t *testing.T, extra schemas.BifrostResponseExtraFields) {
	t.Helper()
	if extra.UpstreamLatency == nil || extra.OverheadLatency == nil {
		t.Fatalf("upstream/overhead latency not populated: upstream=%v overhead=%v", extra.UpstreamLatency, extra.OverheadLatency)
	}
	if got := *extra.UpstreamLatency; got < fishSynthesisDelay.Milliseconds() {
		t.Errorf("upstream latency = %dms, want at least the %dms Fish spent synthesizing (latency=%dms, overhead=%dms)", got, fishSynthesisDelay.Milliseconds(), extra.Latency, *extra.OverheadLatency)
	}
	if got := *extra.OverheadLatency; got >= fishSynthesisDelay.Milliseconds() {
		t.Errorf("overhead latency = %dms includes Fish's synthesis time (upstream=%dms)", got, *extra.UpstreamLatency)
	}
}

func TestFishAudioSpeechReportsUpstreamLatency(t *testing.T) {
	client := newFishLatencyTestClient(t)

	resp, bifrostErr := client.SpeechRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), fishLatencyTestRequest())
	if bifrostErr != nil {
		t.Fatalf("SpeechRequest: %s", bifrostErrMessage(bifrostErr))
	}
	if len(resp.Audio) == 0 {
		t.Fatal("no audio returned")
	}
	assertSynthesisIsUpstream(t, resp.ExtraFields)
}

func TestFishAudioSpeechStreamReportsUpstreamLatency(t *testing.T) {
	client := newFishLatencyTestClient(t)

	stream, bifrostErr := client.SpeechStreamRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), fishLatencyTestRequest())
	if bifrostErr != nil {
		t.Fatalf("SpeechStreamRequest: %s", bifrostErrMessage(bifrostErr))
	}
	var done *schemas.BifrostSpeechStreamResponse
	for chunk := range stream {
		if chunk.BifrostError != nil {
			t.Fatalf("stream error: %s", bifrostErrMessage(chunk.BifrostError))
		}
		if chunk.BifrostSpeechStreamResponse != nil && chunk.BifrostSpeechStreamResponse.Type == schemas.SpeechStreamResponseTypeDone {
			done = chunk.BifrostSpeechStreamResponse
		}
	}
	if done == nil {
		t.Fatal("stream ended without a done chunk")
	}
	assertSynthesisIsUpstream(t, done.ExtraFields)
}
