package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/providers/fishaudio"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"

	"github.com/fasthttp/router"
	ws "github.com/fasthttp/websocket"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// fishRealtimeTestAccount exposes Fish Audio with one key, pointed at a local
// fake tts-live server.
type fishRealtimeTestAccount struct {
	baseURL string
	aliases schemas.KeyAliases
}

func (fishRealtimeTestAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{schemas.FishAudio}, nil
}

func (a fishRealtimeTestAccount) GetKeysForProvider(_ context.Context, p schemas.ModelProvider) ([]schemas.Key, error) {
	if p != schemas.FishAudio {
		return nil, nil
	}
	return []schemas.Key{{
		ID:      "fish-key-1",
		Value:   *schemas.NewSecretVar("fish-test-key"),
		Models:  schemas.WhiteList{"*"},
		Weight:  1.0,
		Aliases: a.aliases,
	}}, nil
}

func (a fishRealtimeTestAccount) GetConfigForProvider(p schemas.ModelProvider) (*schemas.ProviderConfig, error) {
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

// fishTestServerFrame hand-encodes a Fish tts-live server event as a
// MessagePack map (transports has no MessagePack dependency). Keys and values
// alternate; values are short strings or byte slices.
func fishTestServerFrame(keysAndValues ...any) []byte {
	frame := []byte{0x80 | byte(len(keysAndValues)/2)} // fixmap
	for _, kv := range keysAndValues {
		switch v := kv.(type) {
		case string:
			frame = append(frame, 0xa0|byte(len(v))) // fixstr
			frame = append(frame, v...)
		case []byte:
			frame = append(frame, 0xc4, byte(len(v))) // bin 8
			frame = append(frame, v...)
		}
	}
	return frame
}

// scriptedFishTTSLive is a fake tts-live server. It answers flush with one
// audio frame after audioDelay, and stop with finish(stop) and a close
// (sendFinish) or with a bare close.
type scriptedFishTTSLive struct {
	audioDelay time.Duration
	sendFinish bool

	mu     sync.Mutex
	events []string
	closed chan struct{}
}

func startScriptedFishTTSLive(t *testing.T, audioDelay time.Duration, sendFinish bool) (string, *scriptedFishTTSLive) {
	t.Helper()

	fake := &scriptedFishTTSLive{audioDelay: audioDelay, sendFinish: sendFinish, closed: make(chan struct{})}
	// Decodes each upstream frame's event name with Fish's own MessagePack
	// decoder; client events (start, text, flush, stop) come back as their name.
	decoder := &fishaudio.FishAudioProvider{}
	upgrader := ws.FastHTTPUpgrader{CheckOrigin: func(*fasthttp.RequestCtx) bool { return true }}
	r := router.New()
	r.GET("/v1/tts/live", func(ctx *fasthttp.RequestCtx) {
		_ = upgrader.Upgrade(ctx, func(conn *ws.Conn) {
			defer close(fake.closed)
			defer conn.Close()
			for {
				messageType, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				event := "non-binary frame"
				if messageType == ws.BinaryMessage {
					decoded, decodeErr := decoder.ToBifrostRealtimeEvent(data)
					if decodeErr != nil {
						event = "undecodable frame"
					} else {
						event = string(decoded.Type)
					}
				}
				fake.mu.Lock()
				fake.events = append(fake.events, event)
				fake.mu.Unlock()

				switch event {
				case "flush":
					time.Sleep(fake.audioDelay)
					_ = conn.WriteMessage(ws.BinaryMessage, fishTestServerFrame("event", "audio", "audio", []byte{1, 2, 3, 4}))
				case "stop":
					if fake.sendFinish {
						_ = conn.WriteMessage(ws.BinaryMessage, fishTestServerFrame("event", "finish", "reason", "stop"))
						_ = conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
					}
					return
				}
			}
		})
	})

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	return "http://" + ln.Addr().String(), fake
}

func (f *scriptedFishTTSLive) upstreamEvents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// runFishRealtimeTestSession runs a realtime session through the real Fish
// Audio provider against upstream, drives the client side with script, then
// disconnects the client and waits for the session to end. It returns the turn
// responses the post-hooks saw.
func runFishRealtimeTestSession(t *testing.T, baseURL string, script func(client *ws.Conn)) []*schemas.BifrostResponse {
	t.Helper()
	SetLogger(bifrost.NewDefaultLogger(schemas.LogLevelError))

	capture := &realtimeTurnCapturePlugin{}
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:    fishRealtimeTestAccount{baseURL: baseURL},
		Logger:     bifrost.NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{capture},
	})
	require.NoError(t, err)
	defer client.Shutdown()

	pool := bfws.NewPool(&schemas.WSPoolConfig{})
	defer pool.Close()

	h := &WSRealtimeHandler{
		client:       client,
		config:       &lib.Config{Providers: map[schemas.ModelProvider]configstore.ProviderConfig{schemas.FishAudio: {}}},
		handlerStore: testWSHandlerStore{},
		pool:         pool,
	}

	serverSide, clientSide, cleanup := dialRealtimeTestConn(t)
	defer cleanup()
	clientConn := newRealtimeClientConn(serverSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.runRealtimeSession(clientConn, bfws.NewSession(nil), &authHeaders{}, "/v1/realtime", schemas.FishAudio, "s2-pro", "", nil, nil)
	}()

	script(clientSide)

	// realtimeClientConn.Close is a no-op under the test harness, so the client
	// side must disconnect for the session to end.
	_ = clientSide.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("realtime session did not end after the client disconnected")
	}
	return capture.captured()
}

func sendFishRealtimeTestEvent(t *testing.T, client *ws.Conn, event string) {
	t.Helper()
	require.NoError(t, client.WriteMessage(ws.TextMessage, []byte(event)))
}

func fishRealtimeTestItem(t *testing.T, text string) string {
	t.Helper()
	event, err := json.Marshal(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": text}},
		},
	})
	require.NoError(t, err)
	return string(event)
}

// awaitFishRealtimeClientEvent reads client frames until one of type want
// arrives. The client must only ever receive JSON text frames.
func awaitFishRealtimeClientEvent(t *testing.T, client *ws.Conn, want string) {
	t.Helper()
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		messageType, data, err := client.ReadMessage()
		require.NoError(t, err, "waiting for %s", want)
		require.Equal(t, ws.TextMessage, messageType, "the client must receive text frames")
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(data, &event), string(data))
		if event.Type == want {
			return
		}
	}
}

func fishRealtimeTestInputTokens(t *testing.T, resp *schemas.BifrostResponse) int {
	t.Helper()
	require.NotNil(t, resp)
	require.NotNil(t, resp.ResponsesResponse)
	require.NotNil(t, resp.ResponsesResponse.Usage, "a Fish turn must be billed from its input text")
	return resp.ResponsesResponse.Usage.InputTokens
}

const fishRealtimeTestSessionUpdate = `{"type":"session.update","session":{"voice":"voice-abc"}}`

// TestWSRealtimeFishNewInputAfterAudioSplitsTurns drives two Fish turns
// end to end. The relay's first-audio mark is what lets the second input close
// the first turn, with input->first-audio latency; the second turn ends on
// Fish's finish(stop). Each turn is billed at its input's UTF-8 byte count.
func TestWSRealtimeFishNewInputAfterAudioSplitsTurns(t *testing.T) {
	const (
		audioDelay           = 50 * time.Millisecond
		pauseBeforeNextInput = 300 * time.Millisecond
	)
	baseURL, upstream := startScriptedFishTTSLive(t, audioDelay, true)

	responses := runFishRealtimeTestSession(t, baseURL, func(client *ws.Conn) {
		sendFishRealtimeTestEvent(t, client, fishRealtimeTestSessionUpdate)
		sendFishRealtimeTestEvent(t, client, fishRealtimeTestItem(t, "こんにちは"))
		sendFishRealtimeTestEvent(t, client, `{"type":"response.create"}`)
		awaitFishRealtimeClientEvent(t, client, "response.audio.delta")
		time.Sleep(pauseBeforeNextInput)

		sendFishRealtimeTestEvent(t, client, fishRealtimeTestItem(t, "world!"))
		sendFishRealtimeTestEvent(t, client, `{"type":"response.create"}`)
		awaitFishRealtimeClientEvent(t, client, "response.audio.delta")
		sendFishRealtimeTestEvent(t, client, `{"type":"response.cancel"}`)
		awaitFishRealtimeClientEvent(t, client, "response.audio.done")
	})

	require.Equal(t, []string{"start", "text", "flush", "text", "flush", "stop"}, upstream.upstreamEvents())
	require.Len(t, responses, 2, "the input after audio must close the first turn")
	require.Equal(t, len("こんにちは"), fishRealtimeTestInputTokens(t, responses[0]))
	require.Equal(t, len("world!"), fishRealtimeTestInputTokens(t, responses[1]))

	latency := responses[0].ResponsesResponse.ExtraFields.Latency
	require.GreaterOrEqual(t, latency, audioDelay.Milliseconds())
	require.Less(t, latency, pauseBeforeNextInput.Milliseconds(), "latency must be input->first audio, not the whole turn")
}

// TestWSRealtimeFishUpstreamCloseWithoutFinishBillsTurn: Fish drops the socket
// after stop without finish(stop). The session teardown bills the turn.
func TestWSRealtimeFishUpstreamCloseWithoutFinishBillsTurn(t *testing.T) {
	baseURL, upstream := startScriptedFishTTSLive(t, 0, false)

	responses := runFishRealtimeTestSession(t, baseURL, func(client *ws.Conn) {
		sendFishRealtimeTestEvent(t, client, fishRealtimeTestSessionUpdate)
		sendFishRealtimeTestEvent(t, client, fishRealtimeTestItem(t, "こんにちは"))
		sendFishRealtimeTestEvent(t, client, `{"type":"response.create"}`)
		awaitFishRealtimeClientEvent(t, client, "response.audio.delta")
		sendFishRealtimeTestEvent(t, client, `{"type":"response.cancel"}`)
		select {
		case <-upstream.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("the fake upstream did not close")
		}
		// Let the relay see the upstream close before the client disconnects.
		time.Sleep(200 * time.Millisecond)
	})

	require.Equal(t, []string{"start", "text", "flush", "stop"}, upstream.upstreamEvents())
	require.Len(t, responses, 1, "the session teardown must bill the open turn")
	require.Equal(t, len("こんにちは"), fishRealtimeTestInputTokens(t, responses[0]))
}

// TestWSRealtimeFishClientDisconnectBillsTurn: the client disconnects after
// audio arrived and before any finish(stop). The session teardown bills the
// turn.
func TestWSRealtimeFishClientDisconnectBillsTurn(t *testing.T) {
	baseURL, upstream := startScriptedFishTTSLive(t, 0, true)

	responses := runFishRealtimeTestSession(t, baseURL, func(client *ws.Conn) {
		sendFishRealtimeTestEvent(t, client, fishRealtimeTestSessionUpdate)
		sendFishRealtimeTestEvent(t, client, fishRealtimeTestItem(t, "hello"))
		sendFishRealtimeTestEvent(t, client, `{"type":"response.create"}`)
		awaitFishRealtimeClientEvent(t, client, "response.audio.delta")
	})

	require.Equal(t, []string{"start", "text", "flush"}, upstream.upstreamEvents())
	require.Len(t, responses, 1, "the session teardown must bill the open turn")
	require.Equal(t, len("hello"), fishRealtimeTestInputTokens(t, responses[0]))
}
