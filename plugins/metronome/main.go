// Package main exports a native Bifrost plugin for Metronome sandbox ingestion.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
)

type Config struct {
	DryRun    bool   `json:"dry_run"`
	APIKeyEnv string `json:"api_key_env"`
	// Keys are authenticated governance virtual-key UUIDs, never secret VK tokens.
	CustomerMapping map[string]string `json:"customer_mapping"`
	// Explicit single-customer sandbox fallback; leave empty for tenant isolation.
	DefaultCustomerID string `json:"default_customer_id"`
	// Keys are Bifrost provider/model; values match the Metronome rate card exactly.
	ModelMapping    map[string]string `json:"model_mapping"`
	ProviderMapping map[string]string `json:"provider_mapping"`
}

type Event struct {
	TransactionID string     `json:"transaction_id"`
	CustomerID    string     `json:"customer_id"`
	EventType     string     `json:"event_type"`
	Timestamp     string     `json:"timestamp"`
	Properties    TokenUsage `json:"properties"`
}

type TokenUsage struct {
	Model             string `json:"model"`
	Provider          string `json:"provider"`
	InputTokens       int    `json:"input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
	CachedInputTokens int    `json:"cached_input_tokens"`
	CachedWriteTokens int    `json:"cached_write_tokens"`
}

type requestKey string

const attemptKey requestKey = "metronome-attempt-id"

type exporter struct {
	config Config
	apiKey string
	client *http.Client
	queue  chan Event
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

var active *exporter

func GetName() string { return "metronome" }

// Init reads credentials from the process environment, so the plugin API never
// stores or returns the secret. A missing key is fine in the default dry run.
func Init(raw any) error {
	cfg := Config{DryRun: true, APIKeyEnv: "METRONOME_API_KEY"}
	if raw != nil {
		data, err := schemas.MarshalSorted(raw)
		if err != nil {
			return fmt.Errorf("encode metronome config: %w", err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("decode metronome config: %w", err)
		}
	}
	if cfg.APIKeyEnv == "" {
		return fmt.Errorf("metronome api_key_env must not be empty")
	}
	key := strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	if !cfg.DryRun && key == "" {
		return fmt.Errorf("metronome credential environment variable is not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &exporter{config: cfg, apiKey: key, queue: make(chan Event, 1000), ctx: ctx, cancel: cancel,
		client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	active = p
	p.wg.Add(1)
	go p.run()
	log.Printf("[metronome] initialized dry_run=%t", cfg.DryRun)
	return nil
}

func PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error { return nil }

func PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	// A small scalar only: no request or stream content is retained in context.
	if ctx != nil {
		ctx.SetValue(attemptKey, uuid.NewString())
	}
	return req, nil, nil
}

func PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, upstreamErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	p := active
	if p == nil || ctx == nil || resp == nil || upstreamErr != nil {
		return resp, upstreamErr, nil
	}
	extra := resp.GetExtraFields()
	if extra == nil || (extra.CacheDebug != nil && extra.CacheDebug.CacheHit) {
		return resp, upstreamErr, nil
	}
	switch extra.RequestType {
	case schemas.ChatCompletionStreamRequest, schemas.TextCompletionStreamRequest, schemas.ResponsesStreamRequest:
		final, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool)
		if !final {
			return resp, upstreamErr, nil
		}
	case schemas.ChatCompletionRequest, schemas.TextCompletionRequest, schemas.ResponsesRequest:
	default:
		return resp, upstreamErr, nil
	}
	usage, ok := extractUsage(resp)
	if !ok {
		return resp, upstreamErr, nil
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens < 0 || usage.CachedWriteTokens < 0 {
		log.Print("[metronome] skipped inconsistent token counts")
		return resp, upstreamErr, nil
	}
	if usage.InputTokens+usage.OutputTokens+usage.CachedInputTokens+usage.CachedWriteTokens == 0 {
		return resp, upstreamErr, nil
	}
	vkID, _ := ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID).(string)
	customer := p.config.CustomerMapping[vkID]
	if customer == "" {
		customer = p.config.DefaultCustomerID
	}
	if customer == "" {
		log.Print("[metronome] skipped usage: configure customer_mapping for the authenticated virtual-key ID")
		return resp, upstreamErr, nil
	}
	provider, model := string(extra.RoutingInfo.Provider), extra.RoutingInfo.Model
	if provider == "" {
		provider = string(extra.Provider)
	}
	if alias := extra.RoutingInfo.ResolvedKeyAlias; alias != nil {
		model = alias.ModelID
	}
	if model == "" {
		model = extra.ResolvedModelUsed
	}
	if model == "" {
		model = extra.OriginalModelRequested
	}
	if served := extra.RoutingInfo.ServerSideFallbackModel; served != nil && *served != "" {
		model = *served
	}
	if provider == "" || model == "" {
		return resp, upstreamErr, nil
	}
	usage.Provider = provider
	if mapped := p.config.ProviderMapping[provider]; mapped != "" {
		usage.Provider = mapped
	}
	usage.Model = model
	if mapped := p.config.ModelMapping[provider+"/"+model]; mapped != "" {
		usage.Model = mapped
	} else if !strings.Contains(model, "/") {
		// For hosted/cloud/custom providers, configure model_mapping explicitly.
		usage.Model = usage.Provider + "/" + model
	}
	attempt, _ := ctx.Value(attemptKey).(string)
	if attempt == "" {
		log.Print("[metronome] skipped usage: missing pre-hook attempt ID")
		return resp, upstreamErr, nil
	}
	retries, _ := ctx.Value(schemas.BifrostContextKeyNumberOfRetries).(int)
	id := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s:%s", attempt, retries, provider, model)))
	event := Event{TransactionID: fmt.Sprintf("bifrost-%x", id), CustomerID: customer, EventType: "token-billing", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Properties: usage}
	// Copy scalar usage before returning: Bifrost may release pooled responses.
	p.mu.RLock()
	if !p.closed {
		select {
		case p.queue <- event:
		default:
			log.Printf("[metronome] queue full; dropped transaction=%s", event.TransactionID)
		}
	}
	p.mu.RUnlock()
	return resp, upstreamErr, nil
}

func extractUsage(resp *schemas.BifrostResponse) (TokenUsage, bool) {
	var chat *schemas.BifrostLLMUsage
	var responses *schemas.ResponsesResponseUsage
	switch {
	case resp.ChatResponse != nil:
		chat = resp.ChatResponse.Usage
	case resp.TextCompletionResponse != nil:
		chat = resp.TextCompletionResponse.Usage
	case resp.ResponsesResponse != nil:
		responses = resp.ResponsesResponse.Usage
	case resp.ResponsesStreamResponse != nil && resp.ResponsesStreamResponse.Response != nil:
		responses = resp.ResponsesStreamResponse.Response.Usage
	}
	u := TokenUsage{}
	if chat != nil {
		u.InputTokens, u.OutputTokens = chat.PromptTokens, chat.CompletionTokens
		if d := chat.PromptTokensDetails; d != nil {
			u.CachedInputTokens, u.CachedWriteTokens = d.CachedReadTokens, d.CachedWriteTokens
		}
	} else if responses != nil {
		u.InputTokens, u.OutputTokens = responses.InputTokens, responses.OutputTokens
		if d := responses.InputTokensDetails; d != nil {
			u.CachedInputTokens, u.CachedWriteTokens = d.CachedReadTokens, d.CachedWriteTokens
		}
	} else {
		return u, false
	}
	// Bifrost normalizes input totals to include cache reads and writes. Metronome
	// uses separate metrics, so subtract both to avoid billing them twice.
	u.InputTokens -= u.CachedInputTokens + u.CachedWriteTokens
	return u, true
}

func (p *exporter) run() {
	defer p.wg.Done()
	for event := range p.queue {
		if p.ctx.Err() != nil {
			log.Print("[metronome] shutdown deadline reached; pending sandbox events dropped")
			return
		}
		payload, err := schemas.MarshalSorted([]Event{event})
		if err != nil {
			log.Print("[metronome] unable to encode event")
			continue
		}
		if p.config.DryRun {
			log.Printf("[metronome] dry_run %s", payload)
			continue
		}
		if err := p.send(p.ctx, event.TransactionID, payload); err != nil {
			log.Printf("[metronome] dropped transaction=%s: %v", event.TransactionID, err)
		}
	}
}

// send retries the identical serialized payload. External usage propagates errors
// to its caller; the original token exporter still uses its background queue.
func (p *exporter) send(ctx context.Context, id string, payload []byte) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.metronome.com/v1/ingest", bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create ingest request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
		}
		if err == nil && status == http.StatusOK {
			log.Printf("[metronome] accepted transaction=%s", id)
			return nil
		}
		// Keep upstream bodies, credentials and network error URLs out of logs.
		log.Printf("[metronome] ingest failed transaction=%s status=%d attempt=%d", id, status, attempt+1)
		if err == nil && status != 429 && status < 500 {
			return fmt.Errorf("metronome rejected ingestion (HTTP %d)", status)
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
			}
		}
	}
	return fmt.Errorf("metronome retry limit reached")
}

func Cleanup() error {
	p := active
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.queue)
	}
	p.mu.Unlock()
	deadline := time.AfterFunc(5*time.Second, p.cancel)
	p.wg.Wait()
	deadline.Stop()
	p.cancel()
	p.client.CloseIdleConnections()
	return nil
}
