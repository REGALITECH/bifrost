package handlers

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type fishAudioUsageTestPlugin struct {
	name         string
	events       *[]string
	preRequest   *schemas.BifrostRequest
	preContext   *schemas.BifrostContext
	postResponse *schemas.BifrostResponse
	shortCircuit *schemas.LLMPluginShortCircuit
}

func (p *fishAudioUsageTestPlugin) GetName() string { return p.name }
func (p *fishAudioUsageTestPlugin) Cleanup() error  { return nil }
func (p *fishAudioUsageTestPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *fishAudioUsageTestPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	*p.events = append(*p.events, p.name+".pre")
	p.preRequest = req
	p.preContext = ctx
	return req, p.shortCircuit, nil
}
func (p *fishAudioUsageTestPlugin) PostLLMHook(_ *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	*p.events = append(*p.events, p.name+".post")
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
	return NewFishAudioUsageHandler(nil, loggingPlugin, governancePlugin), loggingPlugin, governancePlugin
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
