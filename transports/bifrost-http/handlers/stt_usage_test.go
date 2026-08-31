package handlers

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type sttUsageTestPlugin struct {
	name         string
	events       *[]string
	preRequest   *schemas.BifrostRequest
	preContext   *schemas.BifrostContext
	postResponse *schemas.BifrostResponse
	shortCircuit *schemas.LLMPluginShortCircuit
	preCalls     int
	postCalls    int
}

func (p *sttUsageTestPlugin) GetName() string { return p.name }
func (p *sttUsageTestPlugin) Cleanup() error  { return nil }
func (p *sttUsageTestPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *sttUsageTestPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	*p.events = append(*p.events, p.name+".pre")
	p.preCalls++
	p.preRequest = req
	p.preContext = ctx
	return req, p.shortCircuit, nil
}
func (p *sttUsageTestPlugin) PostLLMHook(_ *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	*p.events = append(*p.events, p.name+".post")
	p.postCalls++
	p.postResponse = response
	return response, bifrostErr, nil
}

func newSTTUsageTestContext(body, virtualKey, requestID string) *fasthttp.RequestCtx {
	var request fasthttp.Request
	request.Header.SetMethod(fasthttp.MethodPost)
	request.SetRequestURI(sttUsagePath)
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

func newSTTUsageTestHandler(events *[]string) (*STTUsageHandler, *sttUsageTestPlugin, *sttUsageTestPlugin) {
	loggingPlugin := &sttUsageTestPlugin{name: "logging", events: events}
	governancePlugin := &sttUsageTestPlugin{name: "governance", events: events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, governancePlugin}
	config.BasePlugins.Store(&plugins)
	return NewSTTUsageHandler(config, "logging", "governance"), loggingPlugin, governancePlugin
}

func TestSTTUsageHandlerRegistersRoute(t *testing.T) {
	events := []string{}
	handler, _, _ := newSTTUsageTestHandler(&events)
	r := router.New()
	handler.RegisterRoutes(r)

	ctx := newSTTUsageTestContext(`{"audio_ms":300,"turns":1,"outcome":"completed","session_id":"session-1","seq":0,"model":"stt-qwen3-asr"}`, "vk-test", "usage-1")
	r.Handler(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestSTTUsageHandlerRecordsTranscriptionUsage(t *testing.T) {
	events := []string{}
	handler, loggingPlugin, governancePlugin := newSTTUsageTestHandler(&events)
	ctx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"vllm/stt-qwen3-asr"}`, "vk-test", "usage-request-1")

	handler.recordUsage(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	var response sttUsageResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &response))
	assert.Equal(t, sttUsageResponse{ID: "usage-request-1", Status: "accepted"}, response)
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)

	require.NotNil(t, loggingPlugin.preRequest)
	assert.Equal(t, schemas.TranscriptionRequest, loggingPlugin.preRequest.RequestType)
	require.NotNil(t, loggingPlugin.preRequest.TranscriptionRequest)
	assert.Equal(t, schemas.VLLM, loggingPlugin.preRequest.TranscriptionRequest.Provider)
	assert.Equal(t, "stt-qwen3-asr", loggingPlugin.preRequest.TranscriptionRequest.Model)
	assert.Nil(t, loggingPlugin.preRequest.TranscriptionRequest.Input, "audio must never enter the logging pipeline")

	require.NotNil(t, governancePlugin.postResponse)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse.Usage)
	assert.Equal(t, "duration", governancePlugin.postResponse.TranscriptionResponse.Usage.Type)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse.Usage.Seconds)
	assert.Equal(t, 5.01, *governancePlugin.postResponse.TranscriptionResponse.Usage.Seconds)
	assert.Equal(t, schemas.TranscriptionRequest, governancePlugin.postResponse.TranscriptionResponse.ExtraFields.RequestType)
	assert.Equal(t, schemas.VLLM, governancePlugin.postResponse.TranscriptionResponse.ExtraFields.Provider)

	dimensions, ok := loggingPlugin.preContext.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "stt", dimensions["usage_kind"])
	assert.Equal(t, "5010", dimensions["audio_ms"])
	assert.Equal(t, "3", dimensions["turns"])
	assert.Equal(t, "completed", dimensions["outcome"])
	assert.Equal(t, "session-1", dimensions["session_id"])
	assert.Equal(t, "7", dimensions["seq"])
	assert.Equal(t, "vk-test", loggingPlugin.preContext.Value(schemas.BifrostContextKeyVirtualKey))
	assert.Equal(t, true, loggingPlugin.preContext.Value(schemas.BifrostContextKeySkipBudgetAndRateLimits))
}

func TestSTTUsageHandlerRejectsUnknownField(t *testing.T) {
	events := []string{}
	handler, _, _ := newSTTUsageTestHandler(&events)
	ctx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"stt-qwen3-asr","text":"secret"}`, "vk-test", "usage-2")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "unknown field")
	assert.Empty(t, events)
}

func TestSTTUsageHandlerRejectsMultipleJSONValues(t *testing.T) {
	events := []string{}
	handler, _, _ := newSTTUsageTestHandler(&events)
	ctx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"stt-qwen3-asr"} {}`, "vk-test", "usage-3")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "multiple JSON values")
	assert.Empty(t, events)
}

func TestSTTUsageHandlerRequiresVirtualKey(t *testing.T) {
	events := []string{}
	handler, _, _ := newSTTUsageTestHandler(&events)
	ctx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"stt-qwen3-asr"}`, "", "usage-4")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusUnauthorized, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "virtual key is required")
	assert.Empty(t, events)
}

func TestSTTUsageHandlerReturnsGovernanceRejection(t *testing.T) {
	events := []string{}
	handler, _, governancePlugin := newSTTUsageTestHandler(&events)
	status := fasthttp.StatusForbidden
	governancePlugin.shortCircuit = &schemas.LLMPluginShortCircuit{Error: &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "virtual key is inactive"},
	}}
	ctx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"stt-qwen3-asr"}`, "vk-test", "usage-5")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusForbidden, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "virtual key is inactive")
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
}

func TestSTTUsageHandlerUsesReloadedGovernancePlugin(t *testing.T) {
	events := []string{}
	loggingPlugin := &sttUsageTestPlugin{name: "logging", events: &events}
	oldGovernancePlugin := &sttUsageTestPlugin{name: "governance", events: &events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, oldGovernancePlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewSTTUsageHandler(config, "logging", "governance")

	firstCtx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"stt-qwen3-asr"}`, "vk-test", "usage-6")
	handler.recordUsage(firstCtx)
	require.Equal(t, fasthttp.StatusAccepted, firstCtx.Response.StatusCode(), string(firstCtx.Response.Body()))
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
	oldGovernanceContext := oldGovernancePlugin.preContext
	oldGovernanceResponse := oldGovernancePlugin.postResponse

	newGovernancePlugin := &sttUsageTestPlugin{name: "governance", events: &events}
	require.NoError(t, config.ReloadPlugin(newGovernancePlugin))
	events = nil

	secondCtx := newSTTUsageTestContext(`{"audio_ms":2500,"turns":1,"outcome":"failed","session_id":"session-2","seq":8,"model":"stt-qwen3-asr"}`, "vk-test", "usage-7")
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

func TestSTTUsageHandlerRequiresBothPlugins(t *testing.T) {
	events := []string{}
	loggingPlugin := &sttUsageTestPlugin{name: "logging", events: &events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewSTTUsageHandler(config, "logging", "governance")
	ctx := newSTTUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"stt-qwen3-asr"}`, "vk-test", "usage-8")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "STT usage recording requires the logging and governance plugins")
	assert.Empty(t, events)
}

func TestSTTUsageValidateRequest(t *testing.T) {
	zero := int64(0)
	negative := int64(-1)
	tests := []struct {
		name        string
		payload     sttUsageRequest
		errorString string
	}{
		{name: "missing audio duration", payload: sttUsageRequest{Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "audio_ms is required and must be non-negative"},
		{name: "negative audio duration", payload: sttUsageRequest{AudioMS: &negative, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "audio_ms is required and must be non-negative"},
		{name: "missing turns", payload: sttUsageRequest{AudioMS: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "turns is required and must be non-negative"},
		{name: "negative turns", payload: sttUsageRequest{AudioMS: &zero, Turns: &negative, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "turns is required and must be non-negative"},
		{name: "missing outcome", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, SessionID: "session", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "outcome must be one of completed or failed"},
		{name: "invalid outcome", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "other", SessionID: "session", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "outcome must be one of completed or failed"},
		{name: "missing session ID", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "session_id is required"},
		{name: "blank session ID", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "  ", Seq: &zero, Model: "stt-qwen3-asr"}, errorString: "session_id is required"},
		{name: "missing sequence", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Model: "stt-qwen3-asr"}, errorString: "seq is required and must be non-negative"},
		{name: "negative sequence", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &negative, Model: "stt-qwen3-asr"}, errorString: "seq is required and must be non-negative"},
		{name: "missing model", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero}, errorString: "model is required"},
		{name: "blank provider model", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "vllm/"}, errorString: "model is required"},
		{name: "wrong provider", payload: sttUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "openai/whisper-1"}, errorString: "model must use the vllm provider"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := validateSTTUsageRequest(&test.payload)
			require.EqualError(t, err, test.errorString)
		})
	}
}
