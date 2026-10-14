package metronome

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const audioBody = `{"billable_bytes":54,"audio_ms":2500,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"s2-pro","occurred_at":"2026-09-15T15:00:00+09:00"}`

func audioContext(vk, id string) *schemas.BifrostContext {
	ctx := schemas.NewBifrostContext(context.Background(), time.Time{})
	ctx.SetValue(schemas.BifrostContextKeyGovernanceVirtualKeyID, vk)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, id)
	return ctx
}

func TestFishAudioEvent(t *testing.T) {
	p := &Plugin{logger: testLogger{}, config: Config{}}
	ctx := audioContext("vk-1", "source-event-1")
	event, err := p.fishAudioEvent(ctx, audioUsage(t, audioBody))
	if err != nil {
		t.Fatal(err)
	}
	if event.CustomerID != "vk-1" || event.EventType != "fishaudio-usage" || event.Timestamp != "2026-09-15T06:00:00Z" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Properties.BillableBytes != 54 || event.Properties.AudioMS != 2500 || event.Properties.Model != "fishaudio/s2-pro" {
		t.Fatalf("unexpected quantities: %+v", event.Properties)
	}
	if len(event.TransactionID) > 128 {
		t.Fatal("transaction ID too long")
	}
	// New exporter and new pre-hook attempt must preserve the external event ID.
	p.PreLLMHook(ctx, nil)
	restarted := &Plugin{logger: testLogger{}, config: p.config}
	again, err := restarted.fishAudioEvent(ctx, audioUsage(t, audioBody))
	if err != nil || event != again {
		t.Fatalf("replay changed event: %+v / %v", again, err)
	}
	for _, pair := range [][2]string{{"vk-2", "source-event-1"}, {"vk-1", "source-event-2"}} {
		other, err := p.fishAudioEvent(audioContext(pair[0], pair[1]), audioUsage(t, audioBody))
		if err != nil || other.TransactionID == event.TransactionID {
			t.Fatalf("identity collision: %+v / %v", other, err)
		}
	}
	// Zero-usage and non-completed reports are observations, not LLM failures.
	for _, outcome := range []string{"completed", "failed", "barged_in", "cache_hit"} {
		body := strings.ReplaceAll(strings.ReplaceAll(audioBody, "completed", outcome), `"billable_bytes":54`, `"billable_bytes":0`)
		got, err := p.fishAudioEvent(ctx, audioUsage(t, body))
		if err != nil || got.Properties.Outcome != outcome || got.Properties.BillableBytes != 0 {
			t.Fatalf("outcome %s: %+v / %v", outcome, got, err)
		}
	}
	p.config.ProviderMapping = map[string]string{"fishaudio": "fish"}
	p.config.ModelMapping = map[string]string{"fishaudio/s2-pro": "voice/pro"}
	mapped, err := p.fishAudioEvent(ctx, audioUsage(t, audioBody))
	if err != nil || mapped.Properties.Provider != "fish" || mapped.Properties.Model != "voice/pro" || mapped.TransactionID != event.TransactionID {
		t.Fatalf("bad mapping: %+v / %v", mapped, err)
	}
}

// Saved external reports are replayed unchanged, so their transaction ID formula
// must never change: SHA-256 of ["bifrost","fishaudio-usage",vk,x-request-id].
func TestFishAudioEventTransactionIDIsStable(t *testing.T) {
	p := &Plugin{logger: testLogger{}, config: Config{}}
	event, err := p.fishAudioEvent(audioContext("vk-1", "source-event-1"), audioUsage(t, audioBody))
	if err != nil {
		t.Fatal(err)
	}
	if want := "bf-fishaudio-815caca2e87c1f7d11b64a13d85c595b3497844695a81e53234d3b4277b8f697"; event.TransactionID != want {
		t.Fatalf("external transaction ID changed: %s", event.TransactionID)
	}
}

func TestFishAudioEventRejectsInvalidReports(t *testing.T) {
	p := &Plugin{logger: testLogger{}, config: Config{}}
	cases := []struct{ name, vk, id, body string }{
		{"unauthenticated", "", "req", audioBody},
		{"blank authenticated ID", "   ", "req", audioBody},
		{"missing id", "vk-1", "", audioBody},
		{"timestamp", "vk-1", "req", strings.ReplaceAll(audioBody, "2026-09-15T15:00:00+09:00", "invalid")},
		{"negative", "vk-1", "req", strings.ReplaceAll(audioBody, `"billable_bytes":54`, `"billable_bytes":-1`)},
		{"missing count", "vk-1", "req", strings.ReplaceAll(audioBody, `"billable_bytes":54,`, ``)},
		{"provider", "vk-1", "req", strings.ReplaceAll(audioBody, `"s2-pro"`, `"openai/tts-1"`)},
		{"outcome", "vk-1", "req", strings.ReplaceAll(audioBody, "completed", "unknown")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := audioContext(tc.vk, tc.id)
			ctx.SetValue(schemas.BifrostContextKeyDimensions, map[string]string{"customer_id": "customer-1"})
			if _, err := p.fishAudioEvent(ctx, audioUsage(t, tc.body)); err == nil {
				t.Fatal("accepted invalid report")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFishAudioHTTPDelivery(t *testing.T) {
	var received [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/ingest" || r.Header.Get("Authorization") != "Bearer sandbox-test-key" {
			t.Error("invalid ingest request")
		}
		body, _ := io.ReadAll(r.Body)
		received = append(received, body)
		if len(received) == 1 {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	t.Setenv("METRONOME_FISHAUDIO_TEST_KEY", "sandbox-test-key")
	p, err := Init(&Config{APIKey: schemas.NewSecretVar("env.METRONOME_FISHAUDIO_TEST_KEY")}, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Cleanup() })
	p.ingestURL = server.URL + "/v1/ingest"
	usage := audioUsage(t, audioBody)
	for range 2 {
		ctx := audioContext("vk-1", "event-1")
		ctx.SetValue(schemas.BifrostContextKeyVirtualKey, "sk-bf-secret")
		receipt, err := p.ReportFishAudio(ctx, usage)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status != "sent" || receipt.TransactionID == "" {
			t.Fatalf("missing acknowledgment: %+v", receipt)
		}
	}
	if len(received) != 3 || string(received[0]) != string(received[1]) || string(received[1]) != string(received[2]) {
		t.Fatalf("retry changed payload: %q", received)
	}
	var events []Event[fishAudioProperties]
	if err := json.Unmarshal(received[0], &events); err != nil || len(events) != 1 || events[0].Properties.BillableBytes != 54 || events[0].CustomerID != "vk-1" {
		t.Fatalf("invalid wire event: %s", received[0])
	}
	for _, secret := range []string{"sandbox-test-key", "sk-bf-secret", "input_tokens"} {
		if strings.Contains(string(received[0]), secret) {
			t.Fatalf("unexpected field: %s", secret)
		}
	}
}

func TestFishAudioHTTPFailure(t *testing.T) {
	p := &Plugin{logger: testLogger{}, config: Config{}, ingestURL: defaultIngestURL, ctx: context.Background()}
	usage := audioUsage(t, audioBody)
	calls := 0
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("secret error body")), Header: make(http.Header)}, nil
	})}
	receipt, err := p.ReportFishAudio(audioContext("vk-1", "event"), usage)
	if err == nil || calls != 1 || receipt != (Receipt{}) || strings.Contains(fmt.Sprint(err), "secret") {
		t.Fatalf("failure acknowledged: calls=%d resp=%+v err=%v", calls, receipt, err)
	}
	// Cancellation never reaches the network.
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := schemas.NewBifrostContext(parent, time.Time{})
	// Parent cancellation reaches BifrostContext through its watcher goroutine.
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not propagate")
	}
	ctx.SetValue(schemas.BifrostContextKeyGovernanceVirtualKeyID, "vk-1")
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "event")
	if _, err := p.ReportFishAudio(ctx, usage); err == nil || calls != 1 {
		t.Fatal("cancelled request sent")
	}
	p.closed = true
	if _, err := p.ReportFishAudio(audioContext("vk-1", "event"), usage); err == nil {
		t.Fatal("closed exporter acknowledged")
	}
}

func TestFishAudioDeliveryRetryExhaustion(t *testing.T) {
	for _, status := range []int{0, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := &Plugin{logger: testLogger{}, config: Config{}, ingestURL: defaultIngestURL, ctx: context.Background()}
			p.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if status == 0 {
					return nil, fmt.Errorf("test connection lost")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}
			receipt, err := p.ReportFishAudio(audioContext("vk-1", "event"), audioUsage(t, audioBody))
			if err == nil || calls != 3 || receipt != (Receipt{}) {
				t.Fatalf("failed delivery acknowledged: calls=%d, err=%v", calls, err)
			}
		})
	}
}

func audioUsage(t *testing.T, body string) *FishAudioUsage {
	t.Helper()
	var usage FishAudioUsage
	if err := json.Unmarshal([]byte(body), &usage); err != nil {
		t.Fatal(err)
	}
	return &usage
}

// realtimeTurn mirrors buildRealtimeTurnPostResponse in the transport: a Fish turn
// arrives as a Responses response whose input tokens are the turn's UTF-8 bytes.
func realtimeTurn(provider schemas.ModelProvider, model string, usage *schemas.ResponsesResponseUsage) *schemas.BifrostResponse {
	resp := &schemas.BifrostResponse{ResponsesResponse: &schemas.BifrostResponsesResponse{Object: "response", Model: model, Usage: usage}}
	resp.PopulateExtraFields(schemas.RealtimeRequest, provider, model, model)
	return resp
}

func realtimeContext(vk, turnID string) *schemas.BifrostContext {
	ctx := audioContext(vk, turnID)
	ctx.SetValue(schemas.BifrostContextKeyParentRequestID, "session-1")
	ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
	return ctx
}

func TestFishAudioRealtimeTurnUsage(t *testing.T) {
	p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 4)}
	ctx := realtimeContext("vk-1", "turn-request-1")
	p.PreLLMHook(ctx, nil)
	turn := realtimeTurn(schemas.FishAudio, "s2.1-pro", &schemas.ResponsesResponseUsage{InputTokens: 54, TotalTokens: 54})
	got, upstreamErr, err := p.PostLLMHook(ctx, turn, nil)
	if err != nil || upstreamErr != nil || got != turn {
		t.Fatalf("changed realtime result: %v / %v", upstreamErr, err)
	}
	if len(p.queue) != 1 {
		t.Fatalf("queued %d events, want exactly one", len(p.queue))
	}
	event := <-p.queue
	props, ok := event.Properties.(fishAudioProperties)
	if !ok || event.EventType != "fishaudio-usage" || event.CustomerID != "vk-1" {
		t.Fatalf("realtime turn not exported as Fish usage: %+v", event)
	}
	want := fishAudioProperties{SchemaVersion: 1, Provider: "fishaudio", Model: "fishaudio/s2.1-pro", UsageSource: "realtime",
		BillableBytes: 54, Outcome: "completed", TurnID: "turn-request-1", SubID: "session-1"}
	if props != want {
		t.Fatalf("realtime properties = %+v, want %+v", props, want)
	}
	if !strings.HasPrefix(event.TransactionID, "bf-fishaudio-realtime-") || len(event.TransactionID) > 128 {
		t.Fatalf("bad realtime transaction ID: %s", event.TransactionID)
	}
	if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
		t.Fatalf("bad timestamp %q", event.Timestamp)
	}
	// An external report whose x-request-id equals the turn ID is a different event.
	external, err := p.fishAudioEvent(audioContext("vk-1", "turn-request-1"), audioUsage(t, audioBody))
	if err != nil || external.TransactionID == event.TransactionID {
		t.Fatalf("realtime ID collides with external report: %v", err)
	}
	// Retrying delivery of the same turn keeps its ID; a new turn gets a new one.
	p.PostLLMHook(ctx, turn, nil)
	if again := <-p.queue; again.TransactionID != event.TransactionID {
		t.Fatal("same turn changed transaction ID")
	}
	next := realtimeContext("vk-1", "turn-request-1")
	p.PreLLMHook(next, nil)
	p.PostLLMHook(next, turn, nil)
	if other := <-p.queue; other.TransactionID == event.TransactionID {
		t.Fatal("separate turns share a transaction ID")
	}
	// Mappings apply as for external reports.
	p.config.ProviderMapping = map[string]string{"fishaudio": "fish"}
	p.config.ModelMapping = map[string]string{"fishaudio/s2.1-pro": "voice/pro"}
	p.PostLLMHook(ctx, turn, nil)
	mapped := <-p.queue
	if props := mapped.Properties.(fishAudioProperties); props.Provider != "fish" || props.Model != "voice/pro" || mapped.TransactionID != event.TransactionID {
		t.Fatalf("bad mapping: %+v", mapped)
	}
}

func TestFishAudioRealtimeTurnSkipped(t *testing.T) {
	usage := func(bytes int) *schemas.ResponsesResponseUsage {
		return &schemas.ResponsesResponseUsage{InputTokens: bytes, TotalTokens: bytes}
	}
	cases := []struct {
		name     string
		vk       string
		preHook  bool
		resp     *schemas.BifrostResponse
		upstream *schemas.BifrostError
	}{
		{"nil usage", "vk-1", true, realtimeTurn(schemas.FishAudio, "s2.1-pro", nil), nil},
		{"zero bytes", "vk-1", true, realtimeTurn(schemas.FishAudio, "s2.1-pro", usage(0)), nil},
		{"missing virtual key", "", true, realtimeTurn(schemas.FishAudio, "s2.1-pro", usage(54)), nil},
		{"blank virtual key", " \t", true, realtimeTurn(schemas.FishAudio, "s2.1-pro", usage(54)), nil},
		{"missing attempt", "vk-1", false, realtimeTurn(schemas.FishAudio, "s2.1-pro", usage(54)), nil},
		// A response with valid usage alongside an error is still a failure.
		{"failed turn", "vk-1", true, realtimeTurn(schemas.FishAudio, "s2.1-pro", usage(54)), &schemas.BifrostError{Error: &schemas.ErrorField{Message: "fish error"}}},
		{"openai turn", "vk-1", true, realtimeTurn(schemas.OpenAI, "gpt-realtime", usage(54)), nil},
		{"elevenlabs turn", "vk-1", true, realtimeTurn(schemas.Elevenlabs, "eleven_flash_v2_5", usage(54)), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 1)}
			ctx := realtimeContext(tc.vk, "turn-request-1")
			if tc.preHook {
				p.PreLLMHook(ctx, nil)
			}
			if _, _, err := p.PostLLMHook(ctx, tc.resp, tc.upstream); err != nil || len(p.queue) != 0 {
				t.Fatalf("realtime turn exported: %v", err)
			}
		})
	}
}

func TestFishAudioRealtimeDelivery(t *testing.T) {
	t.Setenv("METRONOME_TEST_KEY", "test-memory-only-key")
	p, err := Init(&Config{APIKey: schemas.NewSecretVar("env.METRONOME_TEST_KEY")}, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Cleanup() })
	sent := make(chan []byte, 2)
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		sent <- body
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	ctx := realtimeContext("vk-1", "turn-request-1")
	p.PreLLMHook(ctx, nil)
	p.PostLLMHook(ctx, realtimeTurn(schemas.FishAudio, "s2.1-pro", &schemas.ResponsesResponseUsage{InputTokens: 54, TotalTokens: 54}), nil)
	select {
	case body := <-sent:
		var events []Event[fishAudioProperties]
		if err := json.Unmarshal(body, &events); err != nil || len(events) != 1 || events[0].EventType != "fishaudio-usage" ||
			events[0].Properties.BillableBytes != 54 || events[0].Properties.UsageSource != "realtime" {
			t.Fatalf("invalid wire event: %s", body)
		}
		if strings.Contains(string(body), "input_tokens") {
			t.Fatalf("Fish bytes exported as tokens: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime Fish usage was not sent")
	}
}

func TestFishAudioSpeechTransactionID(t *testing.T) {
	p := &Plugin{logger: testLogger{}, queue: make(chan Event[any], 4)}
	speech := &schemas.BifrostResponse{SpeechResponse: &schemas.BifrostSpeechResponse{Usage: &schemas.SpeechUsage{InputChars: 54}}}
	speech.PopulateExtraFields(schemas.SpeechRequest, schemas.FishAudio, "s2-pro", "s2-pro")
	export := func(ctx *schemas.BifrostContext) Event[any] {
		t.Helper()
		p.PostLLMHook(ctx, speech, nil)
		if len(p.queue) != 1 {
			t.Fatalf("queued %d events, want one", len(p.queue))
		}
		return <-p.queue
	}
	ctx := audioContext("vk-1", "source-event-1")
	p.PreLLMHook(ctx, nil)
	event := export(ctx)
	if len(event.TransactionID) > 128 || export(ctx).TransactionID != event.TransactionID {
		t.Fatalf("unstable speech transaction ID: %s", event.TransactionID)
	}
	// The caller's x-request-id never identifies the event: an external report
	// with the same request ID, and another request reusing it, stay distinct.
	external, err := p.fishAudioEvent(ctx, audioUsage(t, audioBody))
	if err != nil || external.TransactionID == event.TransactionID {
		t.Fatalf("speech ID collides with external report: %v", err)
	}
	reused := audioContext("vk-1", "source-event-1")
	p.PreLLMHook(reused, nil)
	if export(reused).TransactionID == event.TransactionID {
		t.Fatal("requests reusing x-request-id share a transaction ID")
	}
	// A core retry is a separate upstream call, as for token events.
	ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, 1)
	if export(ctx).TransactionID == event.TransactionID {
		t.Fatal("retry reused the transaction ID")
	}
}
