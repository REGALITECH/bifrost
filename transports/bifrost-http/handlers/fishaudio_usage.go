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
	"github.com/maximhq/bifrost/plugins/metronome"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const fishAudioUsagePath = "/v1/fishaudio/usage"

// FishAudioUsageHandler records Fish Audio usage that occurred outside Bifrost,
// such as playback of a cached TTS clip. Callers must reuse x-request-id when
// retrying. Logging deduplicates by ID; governance only deduplicates briefly
// in memory. Metronome uses a separate stable transaction ID for delivery.
type FishAudioUsageHandler struct {
	config               *lib.Config
	loggingPluginName    string
	governancePluginName string
}

type fishAudioUsageRequest = metronome.FishAudioUsage

type fishAudioUsageResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	MetronomeStatus string `json:"metronome_status,omitempty"`
	TransactionID   string `json:"transaction_id,omitempty"`
}

func NewFishAudioUsageHandler(config *lib.Config, loggingPluginName, governancePluginName string) *FishAudioUsageHandler {
	return &FishAudioUsageHandler{
		config:               config,
		loggingPluginName:    loggingPluginName,
		governancePluginName: governancePluginName,
	}
}

func (h *FishAudioUsageHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.POST(fishAudioUsagePath, lib.ChainMiddlewares(h.recordUsage, middlewares...))
}

func (h *FishAudioUsageHandler) recordUsage(ctx *fasthttp.RequestCtx) {
	loggingPlugin, _ := lib.FindPluginAs[schemas.LLMPlugin](h.config, h.loggingPluginName)
	governancePlugin, _ := lib.FindPluginAs[schemas.LLMPlugin](h.config, h.governancePluginName)
	if loggingPlugin == nil || governancePlugin == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Fish Audio usage recording requires the logging and governance plugins")
		return
	}

	payload, err := decodeFishAudioUsageRequest(ctx.PostBody())
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	provider, model, err := payload.Validate()
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
	var exporter *metronome.Plugin
	if status, configured := h.config.GetPluginStatusByName(metronome.PluginName); configured && status.Status != schemas.PluginStatusDisabled {
		exporter, err = lib.FindPluginAs[*metronome.Plugin](h.config, metronome.PluginName)
		if err != nil || status.Status != schemas.PluginStatusActive {
			SendError(ctx, fasthttp.StatusServiceUnavailable, "Metronome usage reporting is unavailable")
			return
		}
	}
	if exporter != nil {
		if strings.TrimSpace(string(ctx.Request.Header.Peek("x-request-id"))) == "" {
			SendError(ctx, fasthttp.StatusBadRequest, "x-request-id is required for Metronome; reuse the same ID and payload when retrying")
			return
		}
		if payload.OccurredAt == "" {
			SendError(ctx, fasthttp.StatusBadRequest, "occurred_at in RFC3339 format is required for Metronome; preserve it when retrying")
			return
		}
	}

	requestID, _ := bifrostCtx.Value(schemas.BifrostContextKeyRequestID).(string)
	bifrostCtx.SetValue(schemas.BifrostContextKeySkipBudgetAndRateLimits, true)
	dimensions, _ := bifrostCtx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	dimensions = maps.Clone(dimensions)
	if dimensions == nil {
		dimensions = make(map[string]string, 6)
	}
	dimensions["usage_kind"] = "fishaudio"
	dimensions["billable_bytes"] = strconv.FormatInt(*payload.BillableBytes, 10)
	dimensions["audio_ms"] = strconv.FormatInt(*payload.AudioMS, 10)
	dimensions["outcome"] = payload.Outcome
	dimensions["turn_id"] = payload.TurnID
	dimensions["sub_id"] = payload.SubID
	bifrostCtx.SetValue(schemas.BifrostContextKeyDimensions, dimensions)

	request := &schemas.BifrostRequest{
		RequestType: schemas.SpeechRequest,
		SpeechRequest: &schemas.BifrostSpeechRequest{
			Provider: provider,
			Model:    model,
		},
	}
	response := &schemas.BifrostResponse{
		SpeechResponse: &schemas.BifrostSpeechResponse{
			Usage: &schemas.SpeechUsage{InputChars: int(*payload.BillableBytes)},
		},
	}
	response.PopulateExtraFields(schemas.SpeechRequest, provider, model, model)

	request, _, hookErr := loggingPlugin.PreLLMHook(bifrostCtx, request)
	if hookErr != nil {
		logger.Warn("Fish Audio usage logging pre-hook failed: %v", hookErr)
	}
	request, shortCircuit, hookErr := governancePlugin.PreLLMHook(bifrostCtx, request)
	if hookErr != nil {
		logger.Warn("Fish Audio usage governance pre-hook failed: %v", hookErr)
	}
	if shortCircuit != nil {
		response, bifrostErr := shortCircuit.Response, shortCircuit.Error
		if bifrostErr != nil {
			bifrostErr.PopulateExtraFields(schemas.SpeechRequest, provider, model, model)
		}
		response, bifrostErr = h.runPostHooks(bifrostCtx, loggingPlugin, governancePlugin, response, bifrostErr)
		if bifrostErr != nil {
			SendBifrostError(ctx, bifrostErr)
			return
		}
		if response == nil {
			SendError(ctx, fasthttp.StatusForbidden, "Fish Audio usage recording was rejected")
			return
		}
		SendJSONWithStatus(ctx, fishAudioUsageResponse{ID: requestID, Status: "accepted"}, fasthttp.StatusAccepted)
		return
	}

	response, bifrostErr := h.runPostHooks(bifrostCtx, loggingPlugin, governancePlugin, response, nil)
	if bifrostErr != nil {
		SendBifrostError(ctx, bifrostErr)
		return
	}
	if response == nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Fish Audio usage recording failed")
		return
	}
	receipt := fishAudioUsageResponse{ID: requestID, Status: "accepted"}
	if exporter != nil {
		delivered, err := exporter.ReportFishAudio(bifrostCtx, payload)
		if err != nil {
			logger.Warn("Fish Audio Metronome delivery failed: %v", err)
			SendError(ctx, fasthttp.StatusServiceUnavailable, "Metronome delivery failed; retry with the same x-request-id and payload")
			return
		}
		receipt.MetronomeStatus, receipt.TransactionID = delivered.Status, delivered.TransactionID
	}

	SendJSONWithStatus(ctx, receipt, fasthttp.StatusAccepted)
}

func (h *FishAudioUsageHandler) runPostHooks(ctx *schemas.BifrostContext, loggingPlugin, governancePlugin schemas.LLMPlugin, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
	var err error
	response, bifrostErr, err = governancePlugin.PostLLMHook(ctx, response, bifrostErr)
	if err != nil {
		logger.Warn("Fish Audio usage governance post-hook failed: %v", err)
	}
	response, bifrostErr, err = loggingPlugin.PostLLMHook(ctx, response, bifrostErr)
	if err != nil {
		logger.Warn("Fish Audio usage logging post-hook failed: %v", err)
	}
	return response, bifrostErr
}

func decodeFishAudioUsageRequest(body []byte) (*fishAudioUsageRequest, error) {
	var payload fishAudioUsageRequest
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
