package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/maximhq/bifrost/plugins/metronome"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// These tests run the real governance plugin, so the virtual-key ID Metronome
// bills comes from governance resolving the presented key
// (ResolveAccess -> ResolvePermits -> StampVirtualKeyScope), not from a test
// plugin that sets the context value itself.

// metronomeIngestEvent is the part of a Metronome ingest event these tests read.
type metronomeIngestEvent struct {
	TransactionID string `json:"transaction_id"`
	CustomerID    string `json:"customer_id"`
	EventType     string `json:"event_type"`
}

// metronomeIngestCapture stands in for the Metronome ingest API. The plugin's
// HTTP client has no transport of its own, so it uses http.DefaultTransport.
type metronomeIngestCapture struct {
	bodies chan string
}

func (c *metronomeIngestCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.metronome.com" {
		return nil, fmt.Errorf("unexpected request to %s", req.URL)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	c.bodies <- string(body)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
}

// next returns the next captured ingest body and its single event.
func (c *metronomeIngestCapture) next(t *testing.T) (string, metronomeIngestEvent) {
	t.Helper()
	select {
	case body := <-c.bodies:
		var events []metronomeIngestEvent
		require.NoError(t, json.Unmarshal([]byte(body), &events), body)
		require.Len(t, events, 1, body)
		return body, events[0]
	case <-time.After(10 * time.Second):
		t.Fatal("no Metronome ingest request")
		return "", metronomeIngestEvent{}
	}
}

// startLiveMetronome starts a delivering Metronome plugin whose ingest
// requests go to the returned capture. The plugin is cleaned up before the
// transport is restored.
func startLiveMetronome(t *testing.T) (*metronome.Plugin, *metronomeIngestCapture) {
	t.Helper()
	capture := &metronomeIngestCapture{bodies: make(chan string, 8)}
	previous := http.DefaultTransport
	http.DefaultTransport = capture
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("METRONOME_IDENTITY_TEST_KEY", "test-identity-key")
	plugin, err := metronome.Init(&metronome.Config{APIKey: schemas.NewSecretVar("env.METRONOME_IDENTITY_TEST_KEY")}, testLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, plugin.Cleanup()) })
	return plugin, capture
}

func TestMetronomeFishAudioCustomerIsVirtualKeyIDWithRealGovernance(t *testing.T) {
	SetLogger(testLogger{})
	gov := newUsageBillingGovernance(t, schemas.FishAudio)
	exporter, capture := startLiveMetronome(t)
	calls := []string{}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{&usageHookRecorder{name: "logging", calls: &calls}, gov}
	config.BasePlugins.Store(&plugins)
	require.NoError(t, config.ReloadPlugin(exporter))
	config.UpdatePluginOverallStatus(metronome.PluginName, metronome.PluginName, schemas.PluginStatusActive, nil, nil)
	handler := NewFishAudioUsageHandler(config, "logging", governance.PluginName)

	for _, key := range []usageBillingVK{usageBillingVKA, usageBillingVKB} {
		ctx := newFishAudioUsageTestContext(fishAudioReportBody, key.value, "source-event")
		handler.recordUsage(ctx)
		require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
		var receipt fishAudioUsageResponse
		require.NoError(t, json.Unmarshal(ctx.Response.Body(), &receipt))

		body, event := capture.next(t)
		assert.Equal(t, key.id, event.CustomerID)
		assert.Equal(t, "fishaudio-usage", event.EventType)
		assert.Equal(t, receipt.TransactionID, event.TransactionID)
		assert.NotContains(t, body, key.value)
	}
}

// metronomeIdentityAccount serves OpenAI from a local fake server.
type metronomeIdentityAccount struct{ baseURL string }

func (metronomeIdentityAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{schemas.OpenAI}, nil
}

func (metronomeIdentityAccount) GetKeysForProvider(_ context.Context, p schemas.ModelProvider) ([]schemas.Key, error) {
	if p != schemas.OpenAI {
		return nil, nil
	}
	return []schemas.Key{{ID: "openai-key-1", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 1.0}}, nil
}

func (a metronomeIdentityAccount) GetConfigForProvider(p schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	if p != schemas.OpenAI {
		return nil, fmt.Errorf("unsupported provider %s", p)
	}
	networkConfig := schemas.DefaultNetworkConfig
	networkConfig.BaseURL = a.baseURL
	return &schemas.ProviderConfig{
		NetworkConfig:            networkConfig,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}, nil
}

func TestMetronomeTokenCustomerIsVirtualKeyIDWithRealGovernance(t *testing.T) {
	SetLogger(testLogger{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-mini",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
	}))
	defer upstream.Close()

	gov := newUsageBillingGovernance(t, schemas.OpenAI)
	exporter, capture := startLiveMetronome(t)
	// Governance (builtin order 4) runs before Metronome (order 10), as in the gateway.
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:    metronomeIdentityAccount{baseURL: upstream.URL},
		Logger:     bifrost.NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{gov, exporter},
	})
	require.NoError(t, err)
	defer client.Shutdown()
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}

	for _, key := range []usageBillingVK{usageBillingVKA, usageBillingVKB} {
		var request fasthttp.Request
		request.Header.SetMethod(fasthttp.MethodPost)
		request.SetRequestURI("/v1/chat/completions")
		request.Header.Set("x-bf-vk", key.value)
		requestCtx := &fasthttp.RequestCtx{}
		requestCtx.Init(&request, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)
		bifrostCtx, cancel := lib.ConvertToBifrostContext(requestCtx, config)

		content := "hello"
		response, bifrostErr := client.ChatCompletionRequest(bifrostCtx, &schemas.BifrostChatRequest{
			Provider: schemas.OpenAI,
			Model:    "gpt-4o-mini",
			Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &content}}},
		})
		cancel()
		require.Nil(t, bifrostErr, "%+v", bifrostErr)
		require.NotNil(t, response)

		body, event := capture.next(t)
		assert.Equal(t, key.id, event.CustomerID)
		assert.Equal(t, "token-billing", event.EventType)
		assert.NotContains(t, body, key.value)
	}
}
