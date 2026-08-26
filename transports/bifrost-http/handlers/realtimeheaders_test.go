package handlers

import (
	"context"
	"net"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"

	"github.com/fasthttp/router"
	ws "github.com/fasthttp/websocket"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// headersOnlyRealtimeProvider implements only the upstream RealtimeHeaders.
type headersOnlyRealtimeProvider struct {
	fakeBinaryRealtimeProvider
	headersCalls int
}

func (p *headersOnlyRealtimeProvider) RealtimeHeaders(*schemas.BifrostContext, schemas.Key) (map[string]string, *schemas.BifrostError) {
	p.headersCalls++
	return map[string]string{"x-test-headers": "realtime-headers"}, nil
}

// modelHeadersRealtimeProvider also implements RealtimeModelHeadersProvider.
type modelHeadersRealtimeProvider struct {
	headersOnlyRealtimeProvider
	models []string
}

var _ schemas.RealtimeModelHeadersProvider = (*modelHeadersRealtimeProvider)(nil)

func (p *modelHeadersRealtimeProvider) RealtimeHeadersForModel(_ *schemas.BifrostContext, _ schemas.Key, model string) (map[string]string, *schemas.BifrostError) {
	p.models = append(p.models, model)
	return map[string]string{"x-test-headers": "model-headers", "model": model}, nil
}

func TestRealtimeHeadersForModel_FallsBackToRealtimeHeaders(t *testing.T) {
	provider := &headersOnlyRealtimeProvider{}

	headers, err := realtimeHeadersForModel(nil, provider, schemas.Key{}, "gpt-realtime")

	require.Nil(t, err)
	require.Equal(t, 1, provider.headersCalls, "a provider without RealtimeHeadersForModel must get RealtimeHeaders")
	require.Equal(t, map[string]string{"x-test-headers": "realtime-headers"}, headers)
}

func TestRealtimeHeadersForModel_PassesModel(t *testing.T) {
	provider := &modelHeadersRealtimeProvider{}

	headers, err := realtimeHeadersForModel(nil, provider, schemas.Key{}, "s1")

	require.Nil(t, err)
	require.Equal(t, []string{"s1"}, provider.models)
	require.Zero(t, provider.headersCalls, "RealtimeHeaders must not be called when RealtimeHeadersForModel exists")
	require.Equal(t, "model-headers", headers["x-test-headers"])
	require.Equal(t, "s1", headers["model"])
}

type fishHandshake struct {
	model         string
	authorization string
}

// startFakeFishTTSLive runs a websocket server on Fish's tts-live path that
// reports each handshake's headers and then closes the connection.
func startFakeFishTTSLive(t *testing.T) (string, <-chan fishHandshake) {
	t.Helper()

	handshakes := make(chan fishHandshake, 4)
	upgrader := ws.FastHTTPUpgrader{CheckOrigin: func(*fasthttp.RequestCtx) bool { return true }}
	r := router.New()
	r.GET("/v1/tts/live", func(ctx *fasthttp.RequestCtx) {
		handshake := fishHandshake{
			model:         string(ctx.Request.Header.Peek("model")),
			authorization: string(ctx.Request.Header.Peek("Authorization")),
		}
		_ = upgrader.Upgrade(ctx, func(conn *ws.Conn) {
			handshakes <- handshake
			_ = conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
		})
	})

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	return "http://" + ln.Addr().String(), handshakes
}

// TestWSRealtimeSession_FishReceivesRequestedModel runs a realtime session
// against the real Fish Audio provider and checks that Fish's tts-live
// handshake carries the requested model, after key alias resolution.
func TestWSRealtimeSession_FishReceivesRequestedModel(t *testing.T) {
	SetLogger(bifrost.NewDefaultLogger(schemas.LogLevelError))

	for _, tt := range []struct {
		name      string
		requested string
		aliases   schemas.KeyAliases
		want      string
	}{
		{name: "alias resolved", requested: "etla-tts", aliases: schemas.KeyAliases{"etla-tts": {ModelID: "s1"}}, want: "s1"},
		{name: "model as requested", requested: "s2.1-pro-free", want: "s2.1-pro-free"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			baseURL, handshakes := startFakeFishTTSLive(t)

			client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
				Account: fishRealtimeTestAccount{baseURL: baseURL, aliases: tt.aliases},
				Logger:  bifrost.NewDefaultLogger(schemas.LogLevelError),
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
				h.runRealtimeSession(clientConn, bfws.NewSession(nil), &authHeaders{}, "/v1/realtime", schemas.FishAudio, tt.requested, "", nil, nil)
			}()

			select {
			case handshake := <-handshakes:
				require.Equal(t, tt.want, handshake.model, "Fish must receive the requested model after alias resolution")
				require.Equal(t, "Bearer fish-test-key", handshake.authorization)
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for the Fish tts-live handshake")
			}

			// The fake upstream has closed; the client disconnects too, and the
			// session must end.
			_ = clientSide.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("realtime session did not end after the client and upstream closed")
			}
		})
	}
}
