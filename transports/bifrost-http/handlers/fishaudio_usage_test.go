package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	pluginloader "github.com/maximhq/bifrost/framework/plugins"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type fishAudioUsageTestPlugin struct {
	name              string
	events            *[]string
	preRequest        *schemas.BifrostRequest
	preContext        *schemas.BifrostContext
	postResponse      *schemas.BifrostResponse
	shortCircuit      *schemas.LLMPluginShortCircuit
	preCalls          int
	postCalls         int
	authenticatedVKID string
}

func (p *fishAudioUsageTestPlugin) GetName() string { return p.name }
func (p *fishAudioUsageTestPlugin) Cleanup() error  { return nil }
func (p *fishAudioUsageTestPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *fishAudioUsageTestPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	*p.events = append(*p.events, p.name+".pre")
	p.preCalls++
	p.preRequest = req
	p.preContext = ctx
	if p.authenticatedVKID != "" {
		ctx.SetValue(schemas.BifrostContextKeyGovernanceVirtualKeyID, p.authenticatedVKID)
	}
	return req, p.shortCircuit, nil
}

func (p *fishAudioUsageTestPlugin) PostLLMHook(_ *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	*p.events = append(*p.events, p.name+".post")
	p.postCalls++
	p.postResponse = response
	return response, bifrostErr, nil
}

func newFishAudioUsageTestContext(body, virtualKey, requestID string) *fasthttp.RequestCtx {
	var request fasthttp.Request
	request.Header.SetMethod(fasthttp.MethodPost)
	request.SetRequestURI(fishAudioUsagePath)
	request.Header.SetContentType("application/json")
	request.SetBodyString(body)
	if virtualKey != "" {
		request.Header.Set("x-bf-vk", virtualKey)
	}
	if requestID != "" {
		request.Header.Set("x-request-id", requestID)
	}
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&request, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)
	return ctx
}

func newFishAudioUsageTestHandler(events *[]string) (*FishAudioUsageHandler, *fishAudioUsageTestPlugin, *fishAudioUsageTestPlugin) {
	loggingPlugin := &fishAudioUsageTestPlugin{name: "logging", events: events}
	governancePlugin := &fishAudioUsageTestPlugin{name: "governance", events: events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, governancePlugin}
	config.BasePlugins.Store(&plugins)
	return NewFishAudioUsageHandler(config, "logging", "governance"), loggingPlugin, governancePlugin
}

func TestFishAudioUsageHandlerRegistersRoute(t *testing.T) {
	events := []string{}
	handler, _, _ := newFishAudioUsageTestHandler(&events)
	r := router.New()
	handler.RegisterRoutes(r)

	ctx := newFishAudioUsageTestContext(`{"billable_bytes":12,"audio_ms":300,"outcome":"cache_hit","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro"}`, "vk-test", "usage-1")
	r.Handler(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestFishAudioUsageHandlerRecordsSpeechUsage(t *testing.T) {
	events := []string{}
	handler, loggingPlugin, governancePlugin := newFishAudioUsageTestHandler(&events)
	ctx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"cache_hit","turn_id":"turn-1","sub_id":"sub-1","model":"fishaudio/s2-pro"}`, "vk-test", "usage-request-1")

	handler.recordUsage(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	var response fishAudioUsageResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &response))
	assert.Equal(t, fishAudioUsageResponse{ID: "usage-request-1", Status: "accepted"}, response)
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)

	require.NotNil(t, loggingPlugin.preRequest)
	assert.Equal(t, schemas.SpeechRequest, loggingPlugin.preRequest.RequestType)
	require.NotNil(t, loggingPlugin.preRequest.SpeechRequest)
	assert.Equal(t, schemas.FishAudio, loggingPlugin.preRequest.SpeechRequest.Provider)
	assert.Equal(t, "s2-pro", loggingPlugin.preRequest.SpeechRequest.Model)
	assert.Nil(t, loggingPlugin.preRequest.SpeechRequest.Input, "synthesized text must never enter the logging pipeline")

	require.NotNil(t, governancePlugin.postResponse)
	require.NotNil(t, governancePlugin.postResponse.SpeechResponse)
	require.NotNil(t, governancePlugin.postResponse.SpeechResponse.Usage)
	assert.Equal(t, 42, governancePlugin.postResponse.SpeechResponse.Usage.InputChars)

	dimensions, ok := loggingPlugin.preContext.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "cache_hit", dimensions["outcome"])
	assert.Equal(t, "1250", dimensions["audio_ms"])
	assert.Equal(t, "turn-1", dimensions["turn_id"])
	assert.Equal(t, "sub-1", dimensions["sub_id"])
	assert.Equal(t, "42", dimensions["billable_bytes"])
	assert.Equal(t, "vk-test", loggingPlugin.preContext.Value(schemas.BifrostContextKeyVirtualKey))
	assert.Equal(t, true, loggingPlugin.preContext.Value(schemas.BifrostContextKeySkipBudgetAndRateLimits))
}

func TestFishAudioUsageHandlerRejectsUnknownTextField(t *testing.T) {
	events := []string{}
	handler, _, _ := newFishAudioUsageTestHandler(&events)
	ctx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro","text":"secret"}`, "vk-test", "usage-2")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "unknown field")
	assert.Empty(t, events)
}

func TestFishAudioUsageHandlerRequiresVirtualKey(t *testing.T) {
	events := []string{}
	handler, _, _ := newFishAudioUsageTestHandler(&events)
	ctx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro"}`, "", "usage-3")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusUnauthorized, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "virtual key is required")
	assert.Empty(t, events)
}

func TestFishAudioUsageHandlerRequiresRequestID(t *testing.T) {
	for _, id := range []string{"", "   "} {
		events := []string{}
		handler, _, _ := newFishAudioUsageTestHandler(&events)
		ctx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro"}`, "vk-test", id)
		handler.recordUsage(ctx)
		assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
		assert.Empty(t, events)
	}
}

func TestFishAudioUsageHandlerReturnsGovernanceRejection(t *testing.T) {
	events := []string{}
	handler, _, governancePlugin := newFishAudioUsageTestHandler(&events)
	status := fasthttp.StatusForbidden
	governancePlugin.shortCircuit = &schemas.LLMPluginShortCircuit{Error: &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "virtual key is inactive"},
	}}
	ctx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro"}`, "vk-test", "usage-4")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusForbidden, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "virtual key is inactive")
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
}

func TestFishAudioUsageHandlerUsesReloadedGovernancePlugin(t *testing.T) {
	events := []string{}
	loggingPlugin := &fishAudioUsageTestPlugin{name: "logging", events: &events}
	oldGovernancePlugin := &fishAudioUsageTestPlugin{name: "governance", events: &events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, oldGovernancePlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewFishAudioUsageHandler(config, "logging", "governance")

	firstCtx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro"}`, "vk-test", "usage-5")
	handler.recordUsage(firstCtx)
	require.Equal(t, fasthttp.StatusAccepted, firstCtx.Response.StatusCode(), string(firstCtx.Response.Body()))
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
	oldGovernanceContext := oldGovernancePlugin.preContext
	oldGovernanceResponse := oldGovernancePlugin.postResponse

	newGovernancePlugin := &fishAudioUsageTestPlugin{name: "governance", events: &events}
	require.NoError(t, config.ReloadPlugin(newGovernancePlugin))
	events = nil

	secondCtx := newFishAudioUsageTestContext(`{"billable_bytes":24,"audio_ms":750,"outcome":"cache_hit","turn_id":"turn-2","sub_id":"sub-2","model":"s2-pro"}`, "vk-test", "usage-6")
	handler.recordUsage(secondCtx)

	require.Equal(t, fasthttp.StatusAccepted, secondCtx.Response.StatusCode(), string(secondCtx.Response.Body()))
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
	assert.Same(t, oldGovernanceContext, oldGovernancePlugin.preContext)
	assert.Same(t, oldGovernanceResponse, oldGovernancePlugin.postResponse)
	assert.Equal(t, 1, oldGovernancePlugin.preCalls)
	assert.Equal(t, 1, oldGovernancePlugin.postCalls)
	assert.NotNil(t, newGovernancePlugin.preContext)
	assert.NotNil(t, newGovernancePlugin.postResponse)
	assert.Equal(t, 1, newGovernancePlugin.preCalls)
	assert.Equal(t, 1, newGovernancePlugin.postCalls)
}

func TestFishAudioUsageHandlerRequiresBothPlugins(t *testing.T) {
	events := []string{}
	loggingPlugin := &fishAudioUsageTestPlugin{name: "logging", events: &events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewFishAudioUsageHandler(config, "logging", "governance")
	ctx := newFishAudioUsageTestContext(`{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro"}`, "vk-test", "usage-7")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "Fish Audio usage recording requires the logging and governance plugins")
	assert.Empty(t, events)
}

func TestValidateFishAudioUsageRequest(t *testing.T) {
	validBytes := int64(0)
	validAudioMS := int64(0)
	tests := []struct {
		name    string
		payload fishAudioUsageRequest
	}{
		{name: "missing billable bytes", payload: fishAudioUsageRequest{AudioMS: &validAudioMS, Outcome: "failed", TurnID: "turn", SubID: "sub", Model: "s2-pro"}},
		{name: "missing audio duration", payload: fishAudioUsageRequest{BillableBytes: &validBytes, Outcome: "failed", TurnID: "turn", SubID: "sub", Model: "s2-pro"}},
		{name: "invalid outcome", payload: fishAudioUsageRequest{BillableBytes: &validBytes, AudioMS: &validAudioMS, Outcome: "other", TurnID: "turn", SubID: "sub", Model: "s2-pro"}},
		{name: "wrong provider", payload: fishAudioUsageRequest{BillableBytes: &validBytes, AudioMS: &validAudioMS, Outcome: "failed", TurnID: "turn", SubID: "sub", Model: "openai/tts-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := validateFishAudioUsageRequest(&test.payload)
			assert.Error(t, err)
		})
	}
}

type fishAudioUsageReporter struct {
	post func(*schemas.BifrostContext, *schemas.HTTPRequest, *schemas.HTTPResponse) error
}

func (p *fishAudioUsageReporter) GetName() string { return "metronome" }
func (p *fishAudioUsageReporter) Cleanup() error  { return nil }
func (p *fishAudioUsageReporter) HTTPTransportPreHook(*schemas.BifrostContext, *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return nil, nil
}
func (p *fishAudioUsageReporter) HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	return p.post(ctx, req, resp)
}
func (p *fishAudioUsageReporter) HTTPTransportStreamChunkHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

const fishAudioReportBody = `{"billable_bytes":54,"audio_ms":2500,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro","occurred_at":"2026-09-15T15:00:00+09:00"}`

func TestFishAudioUsageMetronomeDelivery(t *testing.T) {
	for _, delivery := range []string{"sent", "dry_run", "failed", "legacy"} {
		t.Run(delivery, func(t *testing.T) {
			SetLogger(&mockLogger{})
			events := []string{}
			handler, _, governance := newFishAudioUsageTestHandler(&events)
			governance.authenticatedVKID = "vk-uuid"
			reporter := &fishAudioUsageReporter{post: func(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
				assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
				assert.Equal(t, "vk-uuid", ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID))
				assert.Equal(t, "report-id", ctx.Value(schemas.BifrostContextKeyRequestID))
				assert.Equal(t, fishAudioUsagePath, req.Path)
				assert.Empty(t, req.Headers, "never forward secrets")
				var body fishAudioUsageRequest
				require.NoError(t, json.Unmarshal(req.Body, &body))
				assert.Equal(t, "2026-09-15T06:00:00Z", body.OccurredAt)
				assert.EqualValues(t, 54, *body.BillableBytes)
				if delivery == "failed" {
					return fmt.Errorf("test network failure")
				}
				if delivery != "legacy" {
					resp.Headers["x-bf-metronome-status"] = delivery
					resp.Headers["x-bf-metronome-transaction-id"] = "stable-event"
				}
				return nil
			}}
			handler.SetMetronomeResolver(func() (schemas.HTTPTransportPlugin, error) { return reporter, nil })
			ctx := newFishAudioUsageTestContext(fishAudioReportBody, "secret-vk", "report-id")
			handler.recordUsage(ctx)
			if delivery == "failed" || delivery == "legacy" {
				assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
				return
			}
			require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode())
			var receipt fishAudioUsageResponse
			require.NoError(t, json.Unmarshal(ctx.Response.Body(), &receipt))
			assert.Equal(t, delivery, receipt.MetronomeStatus)
			assert.Equal(t, "stable-event", receipt.TransactionID)
		})
	}
}

func TestFishAudioUsageReporterValidationAndReload(t *testing.T) {
	events := []string{}
	handler, _, governance := newFishAudioUsageTestHandler(&events)
	calls := 0
	reporter := &fishAudioUsageReporter{post: func(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
		calls++
		resp.Headers["x-bf-metronome-status"] = "sent"
		resp.Headers["x-bf-metronome-transaction-id"] = "event"
		return nil
	}}
	var current schemas.HTTPTransportPlugin = reporter
	handler.SetMetronomeResolver(func() (schemas.HTTPTransportPlugin, error) { return current, nil })
	ctx := newFishAudioUsageTestContext(`{"billable_bytes":0,"audio_ms":0,"outcome":"failed","turn_id":"t","sub_id":"s","model":"s2-pro"}`, "vk", "id")
	handler.recordUsage(ctx)
	assert.Equal(t, 400, ctx.Response.StatusCode())
	assert.Empty(t, events)
	status := 403
	governance.shortCircuit = &schemas.LLMPluginShortCircuit{Error: &schemas.BifrostError{StatusCode: &status, Error: &schemas.ErrorField{Message: "inactive"}}}
	ctx = newFishAudioUsageTestContext(fishAudioReportBody, "vk", "id")
	handler.recordUsage(ctx)
	assert.Equal(t, 403, ctx.Response.StatusCode())
	assert.Zero(t, calls)
	governance.shortCircuit = nil
	ctx = newFishAudioUsageTestContext(fishAudioReportBody, "vk", "id")
	handler.recordUsage(ctx)
	assert.Equal(t, 202, ctx.Response.StatusCode())
	assert.Equal(t, 1, calls)
	current = nil // unloading is observed on the next request
	ctx = newFishAudioUsageTestContext(fishAudioReportBody, "vk", "id")
	handler.recordUsage(ctx)
	assert.Equal(t, 202, ctx.Response.StatusCode())
	assert.Equal(t, 1, calls)
	handler.SetMetronomeResolver(func() (schemas.HTTPTransportPlugin, error) { return nil, fmt.Errorf("plugin failed to load") })
	ctx = newFishAudioUsageTestContext(fishAudioReportBody, "vk", "id")
	handler.recordUsage(ctx)
	assert.Equal(t, 503, ctx.Response.StatusCode())
	assert.Equal(t, 1, calls)
}

// Build the real .so with the same Go toolchain/flags before running this test.
// This covers symbol discovery and the handler/exporter contract without a key
// or a live Metronome request. See make test-metronome-integration.
func TestFishAudioUsageNativeMetronomePlugin(t *testing.T) {
	path := os.Getenv("BIFROST_TEST_METRONOME_PLUGIN")
	if path == "" {
		t.Skip("set BIFROST_TEST_METRONOME_PLUGIN to a freshly built plugin")
	}
	loader := &pluginloader.SharedObjectPluginLoader{}
	plugin, err := loader.LoadPlugin(path, map[string]any{"dry_run": true, "customer_mapping": map[string]string{"vk-uuid": "sandbox-customer"}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, plugin.Cleanup()) })
	exporter, ok := plugin.(schemas.HTTPTransportPlugin)
	require.True(t, ok)
	events := []string{}
	handler, _, gov := newFishAudioUsageTestHandler(&events)
	gov.authenticatedVKID = "vk-uuid"
	handler.SetMetronomeResolver(func() (schemas.HTTPTransportPlugin, error) { return exporter, nil })
	var first fishAudioUsageResponse
	for range 2 {
		ctx := newFishAudioUsageTestContext(fishAudioReportBody, "secret-vk", "source-event")
		handler.recordUsage(ctx)
		require.Equal(t, 202, ctx.Response.StatusCode(), string(ctx.Response.Body()))
		var receipt fishAudioUsageResponse
		require.NoError(t, json.Unmarshal(ctx.Response.Body(), &receipt))
		require.Equal(t, "dry_run", receipt.MetronomeStatus)
		require.NotEmpty(t, receipt.TransactionID)
		if first.TransactionID != "" {
			assert.Equal(t, first, receipt)
		}
		first = receipt
	}
}
