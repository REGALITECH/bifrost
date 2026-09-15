package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// This contract uses the existing HTTP plugin interface; it requires no core
// schema, provider, or plugin-loader changes. The handler invokes it only after
// governance authentication, with a normalized body and no secret headers.
type fishAudioUsage struct {
	BillableBytes *int64 `json:"billable_bytes"`
	AudioMS       *int64 `json:"audio_ms"`
	Outcome       string `json:"outcome"`
	TurnID        string `json:"turn_id"`
	SubID         string `json:"sub_id"`
	Model         string `json:"model"`
	OccurredAt    string `json:"occurred_at"`
}

type fishAudioProperties struct {
	SchemaVersion int    `json:"schema_version"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	UsageSource   string `json:"usage_source"`
	BillableBytes int64  `json:"billable_bytes"`
	AudioMS       int64  `json:"audio_ms"`
	Outcome       string `json:"outcome"`
	TurnID        string `json:"turn_id"`
	SubID         string `json:"sub_id"`
}

type fishAudioEvent struct {
	TransactionID string              `json:"transaction_id"`
	CustomerID    string              `json:"customer_id"`
	EventType     string              `json:"event_type"`
	Timestamp     string              `json:"timestamp"`
	Properties    fishAudioProperties `json:"properties"`
}

func (p *exporter) fishAudioEvent(ctx *schemas.BifrostContext, body []byte) (fishAudioEvent, error) {
	var event fishAudioEvent
	if ctx == nil {
		return event, fmt.Errorf("missing authenticated context")
	}
	vkID, _ := ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID).(string)
	requestID, _ := ctx.Value(schemas.BifrostContextKeyRequestID).(string)
	if vkID == "" || strings.TrimSpace(requestID) == "" {
		return event, fmt.Errorf("external usage requires an authenticated virtual-key ID and request ID")
	}
	// External reports require an explicit mapping, even when token usage has a
	// default sandbox customer. A reporting identity must not silently change.
	customer := p.config.CustomerMapping[vkID]
	if strings.TrimSpace(customer) == "" {
		return event, fmt.Errorf("configure customer_mapping for the authenticated virtual-key ID")
	}
	var usage fishAudioUsage
	if err := json.Unmarshal(body, &usage); err != nil {
		return event, fmt.Errorf("invalid external usage JSON")
	}
	if usage.BillableBytes == nil || *usage.BillableBytes < 0 || usage.AudioMS == nil || *usage.AudioMS < 0 {
		return event, fmt.Errorf("external usage requires non-negative billable_bytes and audio_ms")
	}
	switch usage.Outcome {
	case "completed", "barged_in", "failed", "cache_hit":
	default:
		return event, fmt.Errorf("invalid external usage outcome")
	}
	if strings.TrimSpace(usage.TurnID) == "" || strings.TrimSpace(usage.SubID) == "" {
		return event, fmt.Errorf("external usage requires turn_id and sub_id")
	}
	at, err := time.Parse(time.RFC3339Nano, usage.OccurredAt)
	if err != nil || at.IsZero() {
		return event, fmt.Errorf("external usage requires occurred_at in RFC3339 format")
	}
	provider, model := schemas.ParseModelString(strings.TrimSpace(usage.Model), schemas.FishAudio)
	if provider != schemas.FishAudio || strings.TrimSpace(model) == "" {
		return event, fmt.Errorf("external usage requires a Fish Audio model")
	}
	// Length-delimited JSON avoids ambiguous concatenation. This ID survives
	// application retries, plugin reloads and process restarts. Never include a
	// timestamp generated at receipt, usage counts, or a new attempt UUID here.
	identity, err := schemas.MarshalSorted([]string{"bifrost", "fishaudio-usage", vkID, requestID})
	if err != nil {
		return event, err
	}
	id := sha256.Sum256(identity)
	props := fishAudioProperties{SchemaVersion: 1, Provider: string(provider), Model: model,
		UsageSource: "external", BillableBytes: *usage.BillableBytes, AudioMS: *usage.AudioMS,
		Outcome: usage.Outcome, TurnID: usage.TurnID, SubID: usage.SubID}
	if mapped := p.config.ProviderMapping[string(provider)]; mapped != "" {
		props.Provider = mapped
	}
	if mapped := p.config.ModelMapping[string(provider)+"/"+model]; mapped != "" {
		props.Model = mapped
	} else if !strings.Contains(model, "/") {
		props.Model = props.Provider + "/" + model
	}
	return fishAudioEvent{TransactionID: fmt.Sprintf("bf-fishaudio-%x", id), CustomerID: customer,
		EventType: "fishaudio-usage", Timestamp: at.UTC().Format(time.RFC3339Nano), Properties: props}, nil
}

// HTTPTransportPostHook sends external usage synchronously. Unlike the token
// queue, it does not acknowledge an event that could be lost on process exit.
// On failure the reporting client must retry the SAME saved event.
func HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	if req == nil || resp == nil || req.Method != http.MethodPost || req.Path != "/v1/fishaudio/usage" || resp.StatusCode != http.StatusAccepted {
		return nil
	}
	p := active
	if p == nil {
		return fmt.Errorf("metronome is not initialized")
	}
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return fmt.Errorf("metronome is shutting down")
	}
	event, err := p.fishAudioEvent(ctx, req.Body)
	if err != nil {
		return err
	}
	payload, err := schemas.MarshalSorted([]fishAudioEvent{event})
	if err != nil {
		return err
	}
	status := "sent"
	if p.config.DryRun {
		log.Printf("[metronome] dry_run %s", payload)
		status = "dry_run"
	} else {
		// A fixed deadline bounds all three attempts. Shutdown also cancels an
		// in-flight external report; no request/body is retained in context.
		sendCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		stop := context.AfterFunc(p.ctx, cancel)
		defer stop()
		defer cancel()
		if err := p.send(sendCtx, event.TransactionID, payload); err != nil {
			return err
		}
	}
	if resp.Headers == nil {
		resp.Headers = make(map[string]string, 2)
	}
	// Explicit acknowledgment lets the handler detect an older .so whose HTTP
	// hook is a no-op, instead of silently claiming successful delivery.
	resp.Headers["x-bf-metronome-status"] = status
	resp.Headers["x-bf-metronome-transaction-id"] = event.TransactionID
	return nil
}
