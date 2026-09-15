package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const fishAudioUsagePath = "/v1/fishaudio/usage"

var fishAudioUsageOutcomes = map[string]struct{}{
	"completed": {},
	"barged_in": {},
	"failed":    {},
	"cache_hit": {},
}

// FishAudioUsageHandler records Fish Audio usage that occurred outside Bifrost,
// such as playback of a cached TTS clip. Callers must reuse x-request-id when
// retrying. Logging deduplicates by ID; governance only deduplicates briefly
// in memory. Metronome uses a separate stable transaction ID for delivery.
type FishAudioUsageHandler struct {
	config               *lib.Config
	loggingPluginName    string
	governancePluginName string
	metronomeResolver    func() (schemas.HTTPTransportPlugin, error)
}

type fishAudioUsageRequest struct {
	BillableBytes *int64 `json:"billable_bytes"`
	AudioMS       *int64 `json:"audio_ms"`
	Outcome       string `json:"outcome"`
	TurnID        string `json:"turn_id"`
	SubID         string `json:"sub_id"`
	Model         string `json:"model"`
	OccurredAt    string `json:"occurred_at,omitempty"`
}

type fishAudioUsageResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	MetronomeStatus string `json:"metronome_status,omitempty"`
	TransactionID   string `json:"transaction_id,omitempty"`
}

// SetMetronomeResolver resolves the current plugin once per report so reloads
// are honored. Only this exporter is invoked, not the full inference pipeline.
func (h *FishAudioUsageHandler) SetMetronomeResolver(resolve func() (schemas.HTTPTransportPlugin, error)) {
	h.metronomeResolver = resolve
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
	if strings.TrimSpace(string(ctx.Request.Header.Peek("x-request-id"))) == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "x-request-id is required; reuse the same ID and payload when retrying")
		return
	}

	payload, err := decodeFishAudioUsageRequest(ctx.PostBody())
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	provider, model, err := validateFishAudioUsageRequest(payload)
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
	var metronome schemas.HTTPTransportPlugin
	if h.metronomeResolver != nil {
		metronome, err = h.metronomeResolver()
		if err != nil {
			SendError(ctx, fasthttp.StatusServiceUnavailable, "Metronome usage reporting is unavailable")
			return
		}
	}
	if metronome != nil && payload.OccurredAt == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "occurred_at in RFC3339 format is required for Metronome; preserve it when retrying")
		return
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
	if payload.OccurredAt != "" {
		dimensions["occurred_at"] = payload.OccurredAt
	}
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
	if metronome != nil {
		// Forward only the validated usage body. In particular, never forward
		// the raw VK, caller dimensions, or arbitrary HTTP headers to the exporter.
		body, err := schemas.MarshalSorted(payload)
		if err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "unable to encode Fish Audio usage")
			return
		}
		req := schemas.AcquireHTTPRequest()
		defer schemas.ReleaseHTTPRequest(req)
		req.Method, req.Path, req.Body = fasthttp.MethodPost, fishAudioUsagePath, body
		resp := schemas.AcquireHTTPResponse()
		defer schemas.ReleaseHTTPResponse(resp)
		resp.StatusCode = fasthttp.StatusAccepted
		if err := metronome.HTTPTransportPostHook(bifrostCtx, req, resp); err != nil {
			logger.Warn("Fish Audio Metronome delivery failed: %v", err)
			SendError(ctx, fasthttp.StatusServiceUnavailable, "Metronome delivery failed; retry with the same x-request-id and payload")
			return
		}
		receipt.MetronomeStatus = resp.Headers["x-bf-metronome-status"]
		receipt.TransactionID = resp.Headers["x-bf-metronome-transaction-id"]
		if resp.StatusCode != fasthttp.StatusAccepted || receipt.TransactionID == "" || (receipt.MetronomeStatus != "sent" && receipt.MetronomeStatus != "dry_run") {
			SendError(ctx, fasthttp.StatusServiceUnavailable, "Metronome did not acknowledge external usage; check that the plugin supports Fish Audio reporting")
			return
		}
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

func validateFishAudioUsageRequest(payload *fishAudioUsageRequest) (schemas.ModelProvider, string, error) {
	if payload.OccurredAt != "" {
		at, err := time.Parse(time.RFC3339Nano, payload.OccurredAt)
		if err != nil || at.IsZero() {
			return "", "", fmt.Errorf("occurred_at must be an RFC3339 timestamp")
		}
		payload.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	}
	if payload.BillableBytes == nil || *payload.BillableBytes < 0 {
		return "", "", fmt.Errorf("billable_bytes is required and must be non-negative")
	}
	if *payload.BillableBytes > int64(math.MaxInt) {
		return "", "", fmt.Errorf("billable_bytes is too large")
	}
	if payload.AudioMS == nil || *payload.AudioMS < 0 {
		return "", "", fmt.Errorf("audio_ms is required and must be non-negative")
	}
	if _, ok := fishAudioUsageOutcomes[payload.Outcome]; !ok {
		return "", "", fmt.Errorf("outcome must be one of completed, barged_in, failed, or cache_hit")
	}
	if strings.TrimSpace(payload.TurnID) == "" {
		return "", "", fmt.Errorf("turn_id is required")
	}
	if strings.TrimSpace(payload.SubID) == "" {
		return "", "", fmt.Errorf("sub_id is required")
	}
	payload.Model = strings.TrimSpace(payload.Model)
	if payload.Model == "" {
		return "", "", fmt.Errorf("model is required")
	}
	provider, model := schemas.ParseModelString(payload.Model, schemas.FishAudio)
	if provider != schemas.FishAudio {
		return "", "", fmt.Errorf("model must use the fishaudio provider")
	}
	if strings.TrimSpace(model) == "" {
		return "", "", fmt.Errorf("model is required")
	}
	return provider, model, nil
}
