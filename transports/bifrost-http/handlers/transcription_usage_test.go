package handlers

import (
	"encoding/json"
	"math"
	"net"
	"strconv"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type transcriptionUsageTestPlugin struct {
	name         string
	events       *[]string
	preRequest   *schemas.BifrostRequest
	preContext   *schemas.BifrostContext
	postResponse *schemas.BifrostResponse
	shortCircuit *schemas.LLMPluginShortCircuit
	preCalls     int
	postCalls    int
}

func (p *transcriptionUsageTestPlugin) GetName() string { return p.name }
func (p *transcriptionUsageTestPlugin) Cleanup() error  { return nil }
func (p *transcriptionUsageTestPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *transcriptionUsageTestPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	*p.events = append(*p.events, p.name+".pre")
	p.preCalls++
	p.preRequest = req
	p.preContext = ctx
	return req, p.shortCircuit, nil
}
func (p *transcriptionUsageTestPlugin) PostLLMHook(_ *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	*p.events = append(*p.events, p.name+".post")
	p.postCalls++
	p.postResponse = response
	return response, bifrostErr, nil
}

func newTranscriptionUsageTestContext(body, virtualKey, requestID string) *fasthttp.RequestCtx {
	var request fasthttp.Request
	request.Header.SetMethod(fasthttp.MethodPost)
	request.SetRequestURI(transcriptionUsagePath)
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

func newTranscriptionUsageTestHandler(events *[]string) (*TranscriptionUsageHandler, *transcriptionUsageTestPlugin, *transcriptionUsageTestPlugin) {
	loggingPlugin := &transcriptionUsageTestPlugin{name: "logging", events: events}
	governancePlugin := &transcriptionUsageTestPlugin{name: "governance", events: events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, governancePlugin}
	config.BasePlugins.Store(&plugins)
	return NewTranscriptionUsageHandler(config, "logging", "governance"), loggingPlugin, governancePlugin
}

func TestTranscriptionUsageHandlerRegistersRoute(t *testing.T) {
	events := []string{}
	handler, _, _ := newTranscriptionUsageTestHandler(&events)
	r := router.New()
	handler.RegisterRoutes(r)

	ctx := newTranscriptionUsageTestContext(`{"audio_ms":300,"turns":1,"outcome":"completed","session_id":"session-1","seq":0,"model":"qwen3-asr"}`, "vk-test", "usage-1")
	r.Handler(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestTranscriptionUsageHandlerRecordsUsage(t *testing.T) {
	events := []string{}
	handler, loggingPlugin, governancePlugin := newTranscriptionUsageTestHandler(&events)
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"vllm/qwen3-asr"}`, "vk-test", "usage-request-1")

	handler.recordUsage(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	var response transcriptionUsageResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &response))
	assert.Equal(t, transcriptionUsageResponse{ID: "usage-request-1", Status: "accepted"}, response)
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)

	require.NotNil(t, loggingPlugin.preRequest)
	assert.Equal(t, schemas.TranscriptionRequest, loggingPlugin.preRequest.RequestType)
	require.NotNil(t, loggingPlugin.preRequest.TranscriptionRequest)
	assert.Equal(t, schemas.VLLM, loggingPlugin.preRequest.TranscriptionRequest.Provider)
	assert.Equal(t, "qwen3-asr", loggingPlugin.preRequest.TranscriptionRequest.Model)
	assert.Nil(t, loggingPlugin.preRequest.TranscriptionRequest.Input, "audio must never enter the logging pipeline")

	require.NotNil(t, governancePlugin.postResponse)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse.Usage)
	assert.Equal(t, "tokens", governancePlugin.postResponse.TranscriptionResponse.Usage.Type)
	assert.Nil(t, governancePlugin.postResponse.TranscriptionResponse.Usage.Seconds)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse.Usage.InputTokens)
	assert.Equal(t, 5010, *governancePlugin.postResponse.TranscriptionResponse.Usage.InputTokens)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse.Usage.OutputTokens)
	assert.Equal(t, 0, *governancePlugin.postResponse.TranscriptionResponse.Usage.OutputTokens)
	require.NotNil(t, governancePlugin.postResponse.TranscriptionResponse.Usage.TotalTokens)
	assert.Equal(t, 5010, *governancePlugin.postResponse.TranscriptionResponse.Usage.TotalTokens)
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

func TestTranscriptionUsageHandlerAcceptsUnknownModel(t *testing.T) {
	events := []string{}
	handler, _, _ := newTranscriptionUsageTestHandler(&events)
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"unknown"}`, "vk-test", "usage-unknown")

	handler.recordUsage(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestTranscriptionUsageHandlerRejectsUnknownField(t *testing.T) {
	events := []string{}
	handler, _, _ := newTranscriptionUsageTestHandler(&events)
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"qwen3-asr","text":"secret"}`, "vk-test", "usage-2")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "unknown field")
	assert.Empty(t, events)
}

func TestTranscriptionUsageHandlerRejectsMultipleJSONValues(t *testing.T) {
	events := []string{}
	handler, _, _ := newTranscriptionUsageTestHandler(&events)
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"qwen3-asr"} {}`, "vk-test", "usage-3")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "multiple JSON values")
	assert.Empty(t, events)
}

func TestTranscriptionUsageHandlerRequiresVirtualKey(t *testing.T) {
	events := []string{}
	handler, _, _ := newTranscriptionUsageTestHandler(&events)
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"qwen3-asr"}`, "", "usage-4")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusUnauthorized, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "virtual key is required")
	assert.Empty(t, events)
}

func TestTranscriptionUsageHandlerReturnsGovernanceRejection(t *testing.T) {
	events := []string{}
	handler, _, governancePlugin := newTranscriptionUsageTestHandler(&events)
	status := fasthttp.StatusForbidden
	governancePlugin.shortCircuit = &schemas.LLMPluginShortCircuit{Error: &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "virtual key is inactive"},
	}}
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"qwen3-asr"}`, "vk-test", "usage-5")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusForbidden, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "virtual key is inactive")
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
}

func TestTranscriptionUsageHandlerUsesReloadedGovernancePlugin(t *testing.T) {
	events := []string{}
	loggingPlugin := &transcriptionUsageTestPlugin{name: "logging", events: &events}
	oldGovernancePlugin := &transcriptionUsageTestPlugin{name: "governance", events: &events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, oldGovernancePlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewTranscriptionUsageHandler(config, "logging", "governance")

	firstCtx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"qwen3-asr"}`, "vk-test", "usage-6")
	handler.recordUsage(firstCtx)
	require.Equal(t, fasthttp.StatusAccepted, firstCtx.Response.StatusCode(), string(firstCtx.Response.Body()))
	assert.Equal(t, []string{"logging.pre", "governance.pre", "governance.post", "logging.post"}, events)
	oldGovernanceContext := oldGovernancePlugin.preContext
	oldGovernanceResponse := oldGovernancePlugin.postResponse

	newGovernancePlugin := &transcriptionUsageTestPlugin{name: "governance", events: &events}
	require.NoError(t, config.ReloadPlugin(newGovernancePlugin))
	events = nil

	secondCtx := newTranscriptionUsageTestContext(`{"audio_ms":2500,"turns":1,"outcome":"failed","session_id":"session-2","seq":8,"model":"qwen3-asr"}`, "vk-test", "usage-7")
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

func TestTranscriptionUsageHandlerRequiresBothPlugins(t *testing.T) {
	events := []string{}
	loggingPlugin := &transcriptionUsageTestPlugin{name: "logging", events: &events}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewTranscriptionUsageHandler(config, "logging", "governance")
	ctx := newTranscriptionUsageTestContext(`{"audio_ms":5010,"turns":3,"outcome":"completed","session_id":"session-1","seq":7,"model":"qwen3-asr"}`, "vk-test", "usage-8")

	handler.recordUsage(ctx)

	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "transcription usage recording requires the logging and governance plugins")
	assert.Empty(t, events)
}

func TestTranscriptionUsageValidateRequest(t *testing.T) {
	zero := int64(0)
	negative := int64(-1)
	tests := []struct {
		name        string
		payload     transcriptionUsageRequest
		errorString string
	}{
		{name: "missing audio duration", payload: transcriptionUsageRequest{Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "qwen3-asr"}, errorString: "audio_ms is required and must be non-negative"},
		{name: "negative audio duration", payload: transcriptionUsageRequest{AudioMS: &negative, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "qwen3-asr"}, errorString: "audio_ms is required and must be non-negative"},
		{name: "missing turns", payload: transcriptionUsageRequest{AudioMS: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "qwen3-asr"}, errorString: "turns is required and must be non-negative"},
		{name: "negative turns", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &negative, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "qwen3-asr"}, errorString: "turns is required and must be non-negative"},
		{name: "missing outcome", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, SessionID: "session", Seq: &zero, Model: "qwen3-asr"}, errorString: "outcome must be one of completed or failed"},
		{name: "invalid outcome", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "other", SessionID: "session", Seq: &zero, Model: "qwen3-asr"}, errorString: "outcome must be one of completed or failed"},
		{name: "missing session ID", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", Seq: &zero, Model: "qwen3-asr"}, errorString: "session_id is required"},
		{name: "blank session ID", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "  ", Seq: &zero, Model: "qwen3-asr"}, errorString: "session_id is required"},
		{name: "missing sequence", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Model: "qwen3-asr"}, errorString: "seq is required and must be non-negative"},
		{name: "negative sequence", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &negative, Model: "qwen3-asr"}, errorString: "seq is required and must be non-negative"},
		{name: "missing model", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero}, errorString: "model is required"},
		{name: "blank provider model", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "vllm/"}, errorString: "model is required"},
		{name: "wrong provider", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "openai/whisper-1"}, errorString: "model must use the vllm provider"},
		{name: "unknown prefix-less model", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "llama-3"}, errorString: "model must be a known ASR usage model"},
		{name: "unknown vllm model", payload: transcriptionUsageRequest{AudioMS: &zero, Turns: &zero, Outcome: "failed", SessionID: "session", Seq: &zero, Model: "vllm/llama-3"}, errorString: "model must be a known ASR usage model"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := validateTranscriptionUsageRequest(&test.payload)
			require.EqualError(t, err, test.errorString)
		})
	}
}

func TestTranscriptionUsageValidateRequestRejectsAudioMSOverflow(t *testing.T) {
	if strconv.IntSize == 64 {
		t.Skip("int64 audio_ms cannot exceed math.MaxInt on 64-bit platforms")
	}

	overflow := int64(math.MaxInt32) + 1
	zero := int64(0)
	payload := transcriptionUsageRequest{
		AudioMS:   &overflow,
		Turns:     &zero,
		Outcome:   "failed",
		SessionID: "session",
		Seq:       &zero,
		Model:     "qwen3-asr",
	}

	_, _, err := validateTranscriptionUsageRequest(&payload)
	require.EqualError(t, err, "audio_ms is too large")
}
