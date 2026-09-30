package handlers

import (
	"context"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"

	ws "github.com/fasthttp/websocket"
	"github.com/stretchr/testify/require"
)

// fakeBinaryUsageRealtimeProvider bills on the turn's input text (one unit per
// byte), like Fish Audio, and reports no usage on its terminal event.
type fakeBinaryUsageRealtimeProvider struct {
	fakeBinaryRealtimeProvider
}

var _ schemas.RealtimeInputUsageProvider = fakeBinaryUsageRealtimeProvider{}

func (fakeBinaryUsageRealtimeProvider) RealtimeInputUsage(inputText string) *schemas.BifrostLLMUsage {
	if inputText == "" {
		return nil
	}
	return &schemas.BifrostLLMUsage{PromptTokens: len(inputText), TotalTokens: len(inputText)}
}

// realtimeTurnCapturePlugin records the responses its PostLLMHook sees.
type realtimeTurnCapturePlugin struct {
	mu        sync.Mutex
	responses []*schemas.BifrostResponse
}

func (p *realtimeTurnCapturePlugin) GetName() string { return "realtime-turn-capture" }
func (p *realtimeTurnCapturePlugin) Cleanup() error  { return nil }
func (p *realtimeTurnCapturePlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}
func (p *realtimeTurnCapturePlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (p *realtimeTurnCapturePlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, resp)
	return resp, bifrostErr, nil
}

func (p *realtimeTurnCapturePlugin) captured() []*schemas.BifrostResponse {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*schemas.BifrostResponse(nil), p.responses...)
}

// TestRealtimeBinaryProviderNewInputAfterOutputClosesTurn checks the turn
// segmentation for binary providers that only signal completion at disconnect:
// a new client input after output finalizes the previous turn, billed from its
// input text, with input->first-output (TTFB) latency.
func TestRealtimeBinaryProviderNewInputAfterOutputClosesTurn(t *testing.T) {
	capture := &realtimeTurnCapturePlugin{}
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:    wsSpanTestAccount{},
		Logger:     bifrost.NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{capture},
	})
	require.NoError(t, err)
	defer client.Shutdown()

	upstreamURL, _ := startFakeRealtimeUpstream(t, nil)
	upstream, err := bfws.DialUpstream(upstreamURL, nil, schemas.ModelProvider("fake-binary"), "key", nil)
	require.NoError(t, err)
	defer upstream.Close()

	serverSide, _, cleanup := dialRealtimeTestConn(t)
	defer cleanup()
	clientConn := newRealtimeClientConn(serverSide)

	h := &WSRealtimeHandler{client: client}
	session := bfws.NewSession(nil)
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	provider := fakeBinaryUsageRealtimeProvider{fakeBinaryRealtimeProvider{binary: true}}

	send := func(message string) {
		t.Helper()
		stop, relayErr := h.processRealtimeClientMessage(clientConn, session, upstream, provider, bifrostCtx, schemas.OpenAI, "fake-model", schemas.Key{}, false, ws.TextMessage, []byte(message))
		require.False(t, stop, "relay stopped: %v", relayErr)
	}

	send(`{"type":"conversation.item.create","item":{"id":"item_1","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`)
	require.Empty(t, capture.captured(), "no turn may be finalized before any output")

	const ttfb = 30 * time.Millisecond
	time.Sleep(ttfb)
	session.MarkRealtimeFirstOutput() // first audio delta of the turn
	time.Sleep(300 * time.Millisecond)

	send(`{"type":"conversation.item.create","item":{"id":"item_2","type":"message","role":"user","content":[{"type":"input_text","text":"world!"}]}}`)

	responses := capture.captured()
	require.Len(t, responses, 1, "the new input after output must finalize exactly the previous turn")
	resp := responses[0]
	require.NotNil(t, resp)
	require.NotNil(t, resp.ResponsesResponse)
	require.NotNil(t, resp.ResponsesResponse.Usage, "turn usage must come from RealtimeInputUsage")
	require.Equal(t, len("hello"), resp.ResponsesResponse.Usage.InputTokens)

	latency := resp.ResponsesResponse.ExtraFields.Latency
	require.GreaterOrEqual(t, latency, ttfb.Milliseconds())
	require.Less(t, latency, int64(300), "latency must be input->first-output, not the whole turn duration")

	// The new input starts the next turn.
	inputs := session.PeekRealtimeTurnInputs()
	require.Len(t, inputs, 1)
	require.Equal(t, "world!", inputs[0].Summary)
	require.True(t, session.RealtimeFirstOutputAt().IsZero(), "first-output mark must reset with the new turn")
}
