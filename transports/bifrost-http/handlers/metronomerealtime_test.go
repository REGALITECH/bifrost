package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/providers/fishaudio"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/metronome"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"
	"github.com/stretchr/testify/require"
)

type metronomeRealtimeAccount struct{}

func (metronomeRealtimeAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return nil, nil
}

func (metronomeRealtimeAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	return nil, nil
}

func (metronomeRealtimeAccount) GetConfigForProvider(schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return nil, fmt.Errorf("not configured")
}

type metronomeCaptureTransport struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (m *metronomeCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.bodies = append(m.bodies, body)
	m.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

// A Fish realtime turn is billed only if the attempt ID minted by the
// Metronome pre-hook reaches the separate post-hook context. Fish turns have no
// explicit start, so finalize runs both hooks: this pins that hand-off through
// the real plugin pipeline and the realtime turn context copy.
func TestMetronomeFishRealtimeTurnUsage(t *testing.T) {
	SetLogger(&mockLogger{})
	capture := &metronomeCaptureTransport{}
	previous := http.DefaultTransport
	http.DefaultTransport = capture
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("METRONOME_REALTIME_TEST_KEY", "test-realtime-key")
	plugin, err := metronome.Init(&metronome.Config{APIKey: schemas.NewSecretVar("env.METRONOME_REALTIME_TEST_KEY")}, &mockLogger{})
	require.NoError(t, err)
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:    metronomeRealtimeAccount{},
		LLMPlugins: []schemas.LLMPlugin{plugin},
		Logger:     &mockLogger{},
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)

	baseCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	baseCtx.SetValue(schemas.BifrostContextKeyGovernanceVirtualKeyID, "vk-uuid")
	session := bfws.NewSession(nil)
	// Record the client's text item the way the websocket relay does.
	message := []byte(`{"type":"conversation.item.create","item":{"id":"item-1","type":"message","role":"user","content":[{"type":"input_text","text":"こんにちは、世界"}]}}`)
	event, err := schemas.ParseRealtimeEvent(message)
	require.NoError(t, err)
	itemID, summary := pendingRealtimeInputUpdate(event)
	require.NotEmpty(t, summary)
	session.RecordRealtimeInput(itemID, summary, string(message))

	fish := &fishaudio.FishAudioProvider{}
	require.Nil(t, finalizeRealtimeTurnHooks(client, baseCtx, session, fish, schemas.FishAudio, "s2.1-pro", &schemas.Key{}, nil, "", realtimeTurnFinalEvent(fish, false), false))
	require.NoError(t, plugin.Cleanup()) // drains the queue

	capture.mu.Lock()
	defer capture.mu.Unlock()
	require.Len(t, capture.bodies, 1)
	var events []struct {
		CustomerID string         `json:"customer_id"`
		EventType  string         `json:"event_type"`
		Properties map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(capture.bodies[0], &events))
	require.Len(t, events, 1)
	require.Equal(t, "fishaudio-usage", events[0].EventType)
	require.Equal(t, "vk-uuid", events[0].CustomerID)
	require.Equal(t, "realtime", events[0].Properties["usage_source"])
	require.Equal(t, "fishaudio/s2.1-pro", events[0].Properties["model"])
	require.EqualValues(t, len("こんにちは、世界"), events[0].Properties["billable_bytes"])
}
