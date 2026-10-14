package metronome

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
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
	// This ID survives application retries, plugin reloads and process restarts.
	// Never include a timestamp generated at receipt, usage counts, or a new
	// attempt UUID here.
	id, err := fishAudioTransactionID("bf-fishaudio-", "bifrost", "fishaudio-usage", vkID, requestID)
	if err != nil {
		return event, err
	}
	props := fishAudioProperties{SchemaVersion: 1, UsageSource: "external", BillableBytes: *usage.BillableBytes,
		AudioMS: *usage.AudioMS, Outcome: usage.Outcome, TurnID: usage.TurnID, SubID: usage.SubID}
	props.Provider, props.Model = p.rateCardNames(string(provider), model)
	return Event[fishAudioProperties]{TransactionID: id, CustomerID: vkID,
		EventType: "fishaudio-usage", Timestamp: usage.OccurredAt, Properties: props}, nil
}

// fishAudioTransactionID hashes length-delimited JSON, which avoids ambiguous
// concatenation of the identity parts.
func fishAudioTransactionID(prefix string, identity ...string) (string, error) {
	encoded, err := schemas.MarshalSorted(identity)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%x", prefix, sha256.Sum256(encoded)), nil
}

// exportFishAudio queues fishaudio-usage for synthesis Bifrost performed itself:
// a Fish Audio realtime turn or speech request. Fish bills UTF-8 input bytes,
// which the realtime turn pipeline reports as input tokens and speech as
// InputChars (bytes when the provider is fishaudio); they are never exported as
// tokens. Delivery shares the token queue, so the realtime relay and speech
// responses never wait on Metronome.
func (p *Plugin) exportFishAudio(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, extra *schemas.BifrostResponseExtraFields, provider, model string) {
	source, billableBytes := "", 0
	switch {
	case extra.RequestType == schemas.RealtimeRequest && resp.ResponsesResponse != nil && resp.ResponsesResponse.Usage != nil:
		source, billableBytes = "realtime", resp.ResponsesResponse.Usage.InputTokens
	case resp.SpeechResponse != nil && resp.SpeechResponse.Usage != nil:
		source, billableBytes = "speech", resp.SpeechResponse.Usage.InputChars
	case resp.SpeechStreamResponse != nil && resp.SpeechStreamResponse.Usage != nil:
		source, billableBytes = "speech", resp.SpeechStreamResponse.Usage.InputChars
	}
	if billableBytes <= 0 {
		return
	}
	vkID, _ := ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID).(string)
	if strings.TrimSpace(vkID) == "" {
		p.logger.Warn("[metronome] skipped Fish Audio usage: missing authenticated virtual-key ID")
		return
	}
	if model == "" {
		return
	}
	// Like token events, identify the attempt by the UUID this plugin's pre-hook
	// mints (once per realtime turn or speech attempt), never by a request ID
	// such as a caller's x-request-id: a collision would make Metronome drop
	// real usage as a duplicate.
	attempt, _ := ctx.Value(attemptKey).(string)
	if attempt == "" {
		p.logger.Warn("[metronome] skipped Fish Audio usage: missing pre-hook attempt ID")
		return
	}
	retries, _ := ctx.Value(schemas.BifrostContextKeyNumberOfRetries).(int)
	id, err := fishAudioTransactionID("bf-fishaudio-"+source+"-", "bifrost", "fishaudio-"+source, vkID, attempt, strconv.Itoa(retries), provider, model)
	if err != nil {
		p.logger.Warn("[metronome] unable to identify Fish Audio usage")
		return
	}
	// Observation only: the Bifrost request (log entry) ID and its session.
	turnID, _ := ctx.Value(schemas.BifrostContextKeyRequestID).(string)
	if strings.TrimSpace(turnID) == "" {
		turnID = attempt
	}
	subID, _ := ctx.Value(schemas.BifrostContextKeyParentRequestID).(string)
	if strings.TrimSpace(subID) == "" {
		subID = turnID
	}
	// Bifrost does not measure Fish output audio; failed requests carry no usage.
	props := fishAudioProperties{SchemaVersion: 1, UsageSource: source, BillableBytes: int64(billableBytes),
		Outcome: "completed", TurnID: turnID, SubID: subID}
	props.Provider, props.Model = p.rateCardNames(provider, model)
	p.enqueue(Event[any]{TransactionID: id, CustomerID: vkID, EventType: "fishaudio-usage",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Properties: props})
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
