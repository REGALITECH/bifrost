package metronome

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// FishAudioUsage is the external usage contract shared by the transport and exporter.
type FishAudioUsage struct {
	BillableBytes *int64 `json:"billable_bytes"`
	AudioMS       *int64 `json:"audio_ms"`
	Outcome       string `json:"outcome"`
	TurnID        string `json:"turn_id"`
	SubID         string `json:"sub_id"`
	Model         string `json:"model"`
	OccurredAt    string `json:"occurred_at,omitempty"`
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

func (p *Plugin) fishAudioEvent(ctx *schemas.BifrostContext, usage *FishAudioUsage) (Event[fishAudioProperties], error) {
	var event Event[fishAudioProperties]
	if ctx == nil {
		return event, fmt.Errorf("missing authenticated context")
	}
	vkID, _ := ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID).(string)
	requestID, _ := ctx.Value(schemas.BifrostContextKeyRequestID).(string)
	if strings.TrimSpace(vkID) == "" || strings.TrimSpace(requestID) == "" {
		return event, fmt.Errorf("external usage requires an authenticated virtual-key ID and request ID")
	}
	provider, model, err := usage.Validate()
	if err != nil {
		return event, err
	}
	if usage.OccurredAt == "" {
		return event, fmt.Errorf("external usage requires occurred_at in RFC3339 format")
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
	return Event[fishAudioProperties]{TransactionID: fmt.Sprintf("bf-fishaudio-%x", id), CustomerID: vkID,
		EventType: "fishaudio-usage", Timestamp: usage.OccurredAt, Properties: props}, nil
}

// Receipt is returned only after successful ingestion.
type Receipt struct {
	Status        string
	TransactionID string
}

// ReportFishAudio waits for ingestion. On failure the caller retries the SAME
// saved event. Only authenticated governance context and typed usage are read.
func (p *Plugin) ReportFishAudio(ctx *schemas.BifrostContext, usage *FishAudioUsage) (Receipt, error) {
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return Receipt{}, fmt.Errorf("metronome is shutting down")
	}
	event, err := p.fishAudioEvent(ctx, usage)
	if err != nil {
		return Receipt{}, err
	}
	payload, err := schemas.MarshalSorted([]Event[fishAudioProperties]{event})
	if err != nil {
		return Receipt{}, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	defer cancel()
	if err := p.send(sendCtx, event.TransactionID, payload); err != nil {
		return Receipt{}, err
	}
	return Receipt{Status: "sent", TransactionID: event.TransactionID}, nil
}

func (payload *FishAudioUsage) Validate() (schemas.ModelProvider, string, error) {
	if payload == nil {
		return "", "", fmt.Errorf("usage is required")
	}
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
	switch payload.Outcome {
	case "completed", "barged_in", "failed", "cache_hit":
	default:
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
