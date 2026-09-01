package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const transcriptionUsagePath = "/v1/audio/transcriptions/usage"

var transcriptionUsageOutcomes = map[string]struct{}{
	"completed": {},
	"failed":    {},
}

// TranscriptionUsageHandler records transcription usage that occurred outside Bifrost. Callers
// must reuse x-request-id when retrying; the existing logging and governance
// hooks use it for deduplication.
type TranscriptionUsageHandler struct {
	config               *lib.Config
	loggingPluginName    string
	governancePluginName string
}

type transcriptionUsageRequest struct {
	AudioMS   *int64 `json:"audio_ms"`
	Turns     *int64 `json:"turns"`
	Outcome   string `json:"outcome"`
	SessionID string `json:"session_id"`
	Seq       *int64 `json:"seq"`
	Model     string `json:"model"`
}

type transcriptionUsageResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func NewTranscriptionUsageHandler(config *lib.Config, loggingPluginName, governancePluginName string) *TranscriptionUsageHandler {
	return &TranscriptionUsageHandler{
		config:               config,
		loggingPluginName:    loggingPluginName,
		governancePluginName: governancePluginName,
	}
}

func (h *TranscriptionUsageHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.POST(transcriptionUsagePath, lib.ChainMiddlewares(h.recordUsage, middlewares...))
}

func (h *TranscriptionUsageHandler) recordUsage(ctx *fasthttp.RequestCtx) {
	loggingPlugin, _ := lib.FindPluginAs[schemas.LLMPlugin](h.config, h.loggingPluginName)
	governancePlugin, _ := lib.FindPluginAs[schemas.LLMPlugin](h.config, h.governancePluginName)
	if loggingPlugin == nil || governancePlugin == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "transcription usage recording requires the logging and governance plugins")
		return
	}

	payload, err := decodeTranscriptionUsageRequest(ctx.PostBody())
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	provider, model, err := validateTranscriptionUsageRequest(payload)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}

	bifrostCtx, cancel := lib.ConvertToBifrostContext(ctx, h.config)
	defer cancel()
	virtualKey, _ := bifrostCtx.Value(schemas.BifrostContextKeyVirtualKey).(string)
	if strings.TrimSpace(virtualKey) == "" {
		SendError(ctx, fasthttp.StatusUnauthorized, "virtual key is required. Provide a virtual key via the x-bf-vk header.")
		return
	}

	requestID, _ := bifrostCtx.Value(schemas.BifrostContextKeyRequestID).(string)
	bifrostCtx.SetValue(schemas.BifrostContextKeySkipBudgetAndRateLimits, true)
	dimensions, _ := bifrostCtx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	dimensions = maps.Clone(dimensions)
	if dimensions == nil {
		dimensions = make(map[string]string, 6)
	}
	dimensions["usage_kind"] = "stt"
	dimensions["audio_ms"] = strconv.FormatInt(*payload.AudioMS, 10)
	dimensions["turns"] = strconv.FormatInt(*payload.Turns, 10)
	dimensions["outcome"] = payload.Outcome
	dimensions["session_id"] = payload.SessionID
	dimensions["seq"] = strconv.FormatInt(*payload.Seq, 10)
	bifrostCtx.SetValue(schemas.BifrostContextKeyDimensions, dimensions)

	request := &schemas.BifrostRequest{
		RequestType: schemas.TranscriptionRequest,
		TranscriptionRequest: &schemas.BifrostTranscriptionRequest{
			Provider: provider,
			Model:    model,
		},
	}
	ms := int(*payload.AudioMS)
	zero := 0
	response := &schemas.BifrostResponse{
		TranscriptionResponse: &schemas.BifrostTranscriptionResponse{
			// External duration is accounted as integer milliseconds; one accounting token equals one millisecond.
			Usage: &schemas.TranscriptionUsage{
				Type:         "tokens",
				InputTokens:  &ms,
				OutputTokens: &zero,
				TotalTokens:  &ms,
			},
		},
	}
	response.PopulateExtraFields(schemas.TranscriptionRequest, provider, model, model)

	request, _, hookErr := loggingPlugin.PreLLMHook(bifrostCtx, request)
	if hookErr != nil {
		logger.Warn("transcription usage logging pre-hook failed: %v", hookErr)
	}
	request, shortCircuit, hookErr := governancePlugin.PreLLMHook(bifrostCtx, request)
	if hookErr != nil {
		logger.Warn("transcription usage governance pre-hook failed: %v", hookErr)
	}
	if shortCircuit != nil {
		response, bifrostErr := shortCircuit.Response, shortCircuit.Error
		if bifrostErr != nil {
			bifrostErr.PopulateExtraFields(schemas.TranscriptionRequest, provider, model, model)
		}
		response, bifrostErr = h.runPostHooks(bifrostCtx, loggingPlugin, governancePlugin, response, bifrostErr)
		if bifrostErr != nil {
			SendBifrostError(ctx, bifrostErr)
			return
		}
		if response == nil {
			SendError(ctx, fasthttp.StatusForbidden, "transcription usage recording was rejected")
			return
		}
		SendJSONWithStatus(ctx, transcriptionUsageResponse{ID: requestID, Status: "accepted"}, fasthttp.StatusAccepted)
		return
	}

	response, bifrostErr := h.runPostHooks(bifrostCtx, loggingPlugin, governancePlugin, response, nil)
	if bifrostErr != nil {
		SendBifrostError(ctx, bifrostErr)
		return
	}
	if response == nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "transcription usage recording failed")
		return
	}
	SendJSONWithStatus(ctx, transcriptionUsageResponse{ID: requestID, Status: "accepted"}, fasthttp.StatusAccepted)
}

func (h *TranscriptionUsageHandler) runPostHooks(ctx *schemas.BifrostContext, loggingPlugin, governancePlugin schemas.LLMPlugin, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
	var err error
	response, bifrostErr, err = governancePlugin.PostLLMHook(ctx, response, bifrostErr)
	if err != nil {
		logger.Warn("transcription usage governance post-hook failed: %v", err)
	}
	response, bifrostErr, err = loggingPlugin.PostLLMHook(ctx, response, bifrostErr)
	if err != nil {
		logger.Warn("transcription usage logging post-hook failed: %v", err)
	}
	return response, bifrostErr
}

func decodeTranscriptionUsageRequest(body []byte) (*transcriptionUsageRequest, error) {
	var payload transcriptionUsageRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid request payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid request format: multiple JSON values")
	}
	return &payload, nil
}

func validateTranscriptionUsageRequest(payload *transcriptionUsageRequest) (schemas.ModelProvider, string, error) {
	if payload.AudioMS == nil || *payload.AudioMS < 0 {
		return "", "", fmt.Errorf("audio_ms is required and must be non-negative")
	}
	if payload.Turns == nil || *payload.Turns < 0 {
		return "", "", fmt.Errorf("turns is required and must be non-negative")
	}
	if _, ok := transcriptionUsageOutcomes[payload.Outcome]; !ok {
		return "", "", fmt.Errorf("outcome must be one of completed or failed")
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return "", "", fmt.Errorf("session_id is required")
	}
	if payload.Seq == nil || *payload.Seq < 0 {
		return "", "", fmt.Errorf("seq is required and must be non-negative")
	}
	payload.Model = strings.TrimSpace(payload.Model)
	if payload.Model == "" {
		return "", "", fmt.Errorf("model is required")
	}
	provider, model := schemas.ParseModelString(payload.Model, schemas.VLLM)
	if provider != schemas.VLLM {
		return "", "", fmt.Errorf("model must use the vllm provider")
	}
	if strings.TrimSpace(model) == "" {
		return "", "", fmt.Errorf("model is required")
	}
	return provider, model, nil
}
