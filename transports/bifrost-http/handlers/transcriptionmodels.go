package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// RegisterModelRoutes uses the same management authentication middleware as
// other /api routes. Tenant virtual keys alone do not grant model administration.
func (h *TranscriptionUsageHandler) RegisterModelRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.POST("/api/transcription/models", lib.ChainMiddlewares(h.ensureModel, middlewares...))
	r.GET("/api/transcription/models", lib.ChainMiddlewares(h.listModels, middlewares...))
	r.GET("/api/transcription/models/{model}", lib.ChainMiddlewares(h.getModel, middlewares...))
	r.PUT("/api/transcription/models/{model}", lib.ChainMiddlewares(h.updateModel, middlewares...))
}

func decodeTranscriptionModelBody(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}

func (h *TranscriptionUsageHandler) modelError(ctx *fasthttp.RequestCtx, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		SendError(ctx, 404, "transcription model not found")
		return
	}
	logger.Warn("transcription model store operation failed: %v", err)
	SendError(ctx, 503, "transcription model store unavailable; reconcile and retry")
}

func (h *TranscriptionUsageHandler) ensureModel(ctx *fasthttp.RequestCtx) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := decodeTranscriptionModelBody(ctx.PostBody(), &payload); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if err := configstore.ValidateTranscriptionModelName(payload.Model); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if err := configstore.EnsureTranscriptionModel(ctx, h.config.ConfigStore, payload.Model); err != nil {
		h.modelError(ctx, err)
		return
	}
	// Refresh the local catalog for management views; accounting reads base prices
	// directly from the shared store so other replicas see registrations at once.
	if h.config.ModelCatalog != nil {
		if err := h.config.ModelCatalog.ReloadPricing(ctx); err != nil {
			h.modelError(ctx, err)
			return
		}
	}
	h.sendModel(ctx, payload.Model)
}

func (h *TranscriptionUsageHandler) sendModel(ctx *fasthttp.RequestCtx, model string) {
	state, err := configstore.GetTranscriptionModel(ctx, h.config.ConfigStore, model)
	if err != nil {
		h.modelError(ctx, err)
		return
	}
	SendJSONWithStatus(ctx, state, 200)
}

func (h *TranscriptionUsageHandler) getModel(ctx *fasthttp.RequestCtx) {
	model, _ := ctx.UserValue("model").(string)
	if err := configstore.ValidateTranscriptionModelName(model); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	h.sendModel(ctx, model)
}

func (h *TranscriptionUsageHandler) listModels(ctx *fasthttp.RequestCtx) {
	states, err := configstore.ListTranscriptionModels(ctx, h.config.ConfigStore)
	if err != nil {
		h.modelError(ctx, err)
		return
	}
	SendJSONWithStatus(ctx, map[string]any{"models": states}, 200)
}

func (h *TranscriptionUsageHandler) updateModel(ctx *fasthttp.RequestCtx) {
	model, _ := ctx.UserValue("model").(string)
	if err := configstore.ValidateTranscriptionModelName(model); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeTranscriptionModelBody(ctx.PostBody(), &payload); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if payload.Enabled == nil {
		SendError(ctx, 400, "enabled is required")
		return
	}
	if err := configstore.SetTranscriptionModelEnabled(ctx, h.config.ConfigStore, model, *payload.Enabled); err != nil {
		h.modelError(ctx, err)
		return
	}
	h.sendModel(ctx, model)
}
