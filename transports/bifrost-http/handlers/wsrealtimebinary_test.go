package handlers

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"

	"github.com/fasthttp/router"
	ws "github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
)

// fakeBinaryRealtimeProvider is a minimal realtime provider whose upstream
// speaks a binary wire format (like Fish Audio's MessagePack tts-live). Client
// events map to non-UTF-8 byte frames; unmapped events map to no frame at all.
type fakeBinaryRealtimeProvider struct {
	binary bool
}

var (
	_ schemas.RealtimeProvider       = fakeBinaryRealtimeProvider{}
	_ schemas.RealtimeBinaryProvider = fakeBinaryRealtimeProvider{}
)

// Upstream -> client frame markers understood by ToBifrostRealtimeEvent.
const (
	fakeBinaryFrameAudio   byte = 0xA0 // audio delta, forwarded with RawData
	fakeBinaryFrameIgnored byte = 0xA1 // not forwarded
	fakeBinaryFrameNoRaw   byte = 0xA2 // forwarded but carries no client payload
	fakeBinaryFrameDone    byte = 0xA3 // terminal event, forwarded with RawData
)

func (p fakeBinaryRealtimeProvider) RealtimeUsesBinaryProtocol() bool { return p.binary }
func (fakeBinaryRealtimeProvider) SupportsRealtimeAPI() bool          { return true }
func (fakeBinaryRealtimeProvider) RealtimeWebSocketURL(schemas.Key, string, string) (string, *schemas.BifrostError) {
	return "", nil
}
func (fakeBinaryRealtimeProvider) RealtimeHeaders(*schemas.BifrostContext, schemas.Key) (map[string]string, *schemas.BifrostError) {
	return nil, nil
}
func (fakeBinaryRealtimeProvider) SupportsRealtimeWebRTC() bool { return false }
func (fakeBinaryRealtimeProvider) ExchangeRealtimeWebRTCSDP(*schemas.BifrostContext, schemas.Key, string, string, json.RawMessage) (string, *schemas.BifrostError) {
	return "", nil
}
func (fakeBinaryRealtimeProvider) ShouldStartRealtimeTurn(*schemas.BifrostRealtimeEvent) bool {
	return false
}
func (fakeBinaryRealtimeProvider) RealtimeTurnFinalEvent() schemas.RealtimeEventType {
	return schemas.RTEventResponseAudioDone
}
func (fakeBinaryRealtimeProvider) RealtimeWebRTCDataChannelLabel() string { return "" }
func (fakeBinaryRealtimeProvider) RealtimeWebSocketSubprotocol() string   { return "" }
func (fakeBinaryRealtimeProvider) ShouldForwardRealtimeEvent(event *schemas.BifrostRealtimeEvent) bool {
	return event.Type == schemas.RTEventResponseAudioDelta || event.Type == schemas.RTEventResponseAudioDone
}
func (fakeBinaryRealtimeProvider) ShouldAccumulateRealtimeOutput(schemas.RealtimeEventType) bool {
	return false
}

func (p fakeBinaryRealtimeProvider) ToProviderRealtimeEvent(event *schemas.BifrostRealtimeEvent) (json.RawMessage, error) {
	if !p.binary {
		return json.RawMessage(`{"type":"` + string(event.Type) + `"}`), nil
	}
	switch event.Type {
	case schemas.RTEventSessionUpdate:
		return json.RawMessage{0x81, 0xFF, 0x01}, nil
	case schemas.RTEventConversationItemCreate:
		return json.RawMessage{0x82, 0xFE, 0x02}, nil
	case schemas.RTEventResponseCreate:
		return json.RawMessage{0x83, 0xFD, 0x03}, nil
	default:
		return nil, nil // unmapped: no upstream frame
	}
}

func (fakeBinaryRealtimeProvider) ToBifrostRealtimeEvent(frame json.RawMessage) (*schemas.BifrostRealtimeEvent, error) {
	if len(frame) == 0 {
		return &schemas.BifrostRealtimeEvent{Type: "empty"}, nil
	}
	switch frame[0] {
	case fakeBinaryFrameAudio:
		return &schemas.BifrostRealtimeEvent{
			Type:    schemas.RTEventResponseAudioDelta,
			Delta:   &schemas.RealtimeDelta{Audio: "AAE="},
			RawData: json.RawMessage(`{"type":"response.audio.delta","delta":"AAE="}`),
		}, nil
	case fakeBinaryFrameNoRaw:
		return &schemas.BifrostRealtimeEvent{Type: schemas.RTEventResponseAudioDelta}, nil
	case fakeBinaryFrameDone:
		return &schemas.BifrostRealtimeEvent{
			Type:    schemas.RTEventResponseAudioDone,
			RawData: json.RawMessage(`{"type":"response.audio.done"}`),
		}, nil
	default:
		return &schemas.BifrostRealtimeEvent{Type: "fake.ignored"}, nil
	}
}

type recordedRealtimeFrame struct {
	messageType int
	data        []byte
}

// startFakeRealtimeUpstream runs a websocket server that records every frame it
// receives and, once a client connects, sends the given frames and closes.
func startFakeRealtimeUpstream(t *testing.T, send []recordedRealtimeFrame) (string, func() []recordedRealtimeFrame) {
	t.Helper()

	var mu sync.Mutex
	var received []recordedRealtimeFrame
	upgrader := ws.FastHTTPUpgrader{CheckOrigin: func(*fasthttp.RequestCtx) bool { return true }}
	r := router.New()
	r.GET("/live", func(ctx *fasthttp.RequestCtx) {
		_ = upgrader.Upgrade(ctx, func(conn *ws.Conn) {
			for _, frame := range send {
				if err := conn.WriteMessage(frame.messageType, frame.data); err != nil {
					return
				}
			}
			if len(send) > 0 {
				_ = conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
			}
			for {
				messageType, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				mu.Lock()
				received = append(received, recordedRealtimeFrame{messageType, append([]byte(nil), data...)})
				mu.Unlock()
			}
		})
	})

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	return "ws://" + ln.Addr().String() + "/live", func() []recordedRealtimeFrame {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRealtimeFrame(nil), received...)
	}
}

// relayClientEventsToFakeUpstream relays four client events (one of which the
// binary provider leaves unmapped) through processRealtimeClientMessage and
// returns the frames the upstream received once lastFrame(data) matches.
func relayClientEventsToFakeUpstream(t *testing.T, provider fakeBinaryRealtimeProvider, lastFrame func([]byte) bool) []recordedRealtimeFrame {
	t.Helper()

	upstreamURL, received := startFakeRealtimeUpstream(t, nil)
	upstream, err := bfws.DialUpstream(upstreamURL, nil, schemas.ModelProvider("fake-binary"), "key", nil)
	if err != nil {
		t.Fatalf("dial upstream: %v", err)
	}
	defer upstream.Close()

	serverSide, _, cleanup := dialRealtimeTestConn(t)
	defer cleanup()
	clientConn := newRealtimeClientConn(serverSide)

	h := &WSRealtimeHandler{}
	session := bfws.NewSession(nil)
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	for _, message := range []string{
		`{"type":"session.update","session":{"voice":"abc"}}`,
		`{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`,
		`{"type":"input_audio_buffer.clear"}`,
		`{"type":"response.create"}`,
	} {
		stop, relayErr := h.processRealtimeClientMessage(clientConn, session, upstream, provider, bifrostCtx, schemas.ModelProvider("fake-binary"), "fake-model", schemas.Key{}, false, ws.TextMessage, []byte(message))
		if stop {
			t.Fatalf("relay stopped on %s: %v", message, relayErr)
		}
	}

	// Frames arrive in order, so once the frame for the last client event is in,
	// any frame for the earlier unmapped event would already have been recorded.
	deadline := time.Now().Add(5 * time.Second)
	for {
		frames := received()
		if n := len(frames); n > 0 && lastFrame(frames[n-1].data) {
			return frames
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for upstream frames, got %d", len(frames))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRealtimeRelayWritesBinaryFramesForBinaryProvider(t *testing.T) {
	frames := relayClientEventsToFakeUpstream(t, fakeBinaryRealtimeProvider{binary: true}, func(data []byte) bool {
		return len(data) > 0 && data[0] == 0x83 // response.create
	})

	if len(frames) != 3 {
		t.Fatalf("expected 3 upstream frames (unmapped event skipped), got %d", len(frames))
	}
	for i, frame := range frames {
		if frame.messageType != ws.BinaryMessage {
			t.Errorf("upstream frame %d: type = %d, want binary (%d)", i, frame.messageType, ws.BinaryMessage)
		}
		if len(frame.data) == 0 {
			t.Errorf("upstream frame %d: empty frame written for an unmapped client event", i)
		}
	}
}

func TestRealtimeRelayKeepsTextFramesForJSONProvider(t *testing.T) {
	frames := relayClientEventsToFakeUpstream(t, fakeBinaryRealtimeProvider{binary: false}, func(data []byte) bool {
		return string(data) == `{"type":"response.create"}`
	})

	if len(frames) != 4 {
		t.Fatalf("expected 4 upstream frames for a JSON provider, got %d", len(frames))
	}
	for i, frame := range frames {
		if frame.messageType != ws.TextMessage {
			t.Errorf("upstream frame %d: type = %d, want text (%d)", i, frame.messageType, ws.TextMessage)
		}
	}
}

func TestRealtimeRelayForwardsBinaryProviderEventsAsText(t *testing.T) {
	upstreamURL, _ := startFakeRealtimeUpstream(t, []recordedRealtimeFrame{
		{ws.BinaryMessage, []byte{fakeBinaryFrameAudio, 0xFF}},
		{ws.BinaryMessage, []byte{fakeBinaryFrameIgnored, 0xFF}},
		{ws.BinaryMessage, []byte{fakeBinaryFrameNoRaw, 0xFF}},
		{ws.BinaryMessage, []byte{fakeBinaryFrameDone, 0xFF}},
	})
	upstream, err := bfws.DialUpstream(upstreamURL, nil, schemas.ModelProvider("fake-binary"), "key", nil)
	if err != nil {
		t.Fatalf("dial upstream: %v", err)
	}
	defer upstream.Close()

	serverSide, client, cleanup := dialRealtimeTestConn(t)
	defer cleanup()
	clientConn := newRealtimeClientConn(serverSide)

	h := &WSRealtimeHandler{}
	session := bfws.NewSession(nil)
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	relayDone := make(chan error, 1)
	go func() {
		relayDone <- h.relayRealtimeProviderToClient(clientConn, session, upstream, fakeBinaryRealtimeProvider{binary: true}, bifrostCtx, schemas.ModelProvider("fake-binary"), "fake-model", schemas.Key{}, false)
	}()

	want := []string{
		`{"type":"response.audio.delta","delta":"AAE="}`,
		`{"type":"response.audio.done"}`,
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i, expected := range want {
		messageType, data, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("client read %d: %v", i, err)
		}
		if messageType != ws.TextMessage {
			t.Errorf("client frame %d: type = %d, want text (%d)", i, messageType, ws.TextMessage)
		}
		if string(data) != expected {
			t.Errorf("client frame %d = %s, want %s", i, data, expected)
		}
	}

	select {
	case <-relayDone:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not stop after the upstream closed")
	}

	// Nothing else (ignored or payload-less events) may have reached the client.
	_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if messageType, data, err := client.ReadMessage(); err == nil {
		t.Fatalf("unexpected extra client frame: type=%d data=%q", messageType, data)
	}
}
