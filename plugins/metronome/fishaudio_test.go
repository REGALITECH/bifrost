package main

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
	p := &exporter{config: Config{CustomerMapping: map[string]string{"vk-1": "customer-1", "vk-2": "customer-1"}}}
	ctx := audioContext("vk-1", "source-event-1")
	event, err := p.fishAudioEvent(ctx, []byte(audioBody))
	if err != nil {
		t.Fatal(err)
	}
	if event.CustomerID != "customer-1" || event.EventType != "fishaudio-usage" || event.Timestamp != "2026-09-15T06:00:00Z" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Properties.BillableBytes != 54 || event.Properties.AudioMS != 2500 || event.Properties.Model != "fishaudio/s2-pro" {
		t.Fatalf("unexpected quantities: %+v", event.Properties)
	}
	if len(event.TransactionID) > 128 {
		t.Fatal("transaction ID too long")
	}
	// New exporter and new pre-hook attempt must preserve the external event ID.
	PreLLMHook(ctx, nil)
	restarted := &exporter{config: p.config}
	again, err := restarted.fishAudioEvent(ctx, []byte(audioBody))
	if err != nil || event != again {
		t.Fatalf("replay changed event: %+v / %v", again, err)
	}
	for _, pair := range [][2]string{{"vk-2", "source-event-1"}, {"vk-1", "source-event-2"}} {
		other, err := p.fishAudioEvent(audioContext(pair[0], pair[1]), []byte(audioBody))
		if err != nil || other.TransactionID == event.TransactionID {
			t.Fatalf("identity collision: %+v / %v", other, err)
		}
	}
	// Zero-usage and non-completed reports are observations, not LLM failures.
	for _, outcome := range []string{"completed", "failed", "barged_in", "cache_hit"} {
		body := strings.ReplaceAll(strings.ReplaceAll(audioBody, "completed", outcome), `"billable_bytes":54`, `"billable_bytes":0`)
		got, err := p.fishAudioEvent(ctx, []byte(body))
		if err != nil || got.Properties.Outcome != outcome || got.Properties.BillableBytes != 0 {
			t.Fatalf("outcome %s: %+v / %v", outcome, got, err)
		}
	}
	p.config.ProviderMapping = map[string]string{"fishaudio": "fish"}
	p.config.ModelMapping = map[string]string{"fishaudio/s2-pro": "voice/pro"}
	mapped, err := p.fishAudioEvent(ctx, []byte(audioBody))
	if err != nil || mapped.Properties.Provider != "fish" || mapped.Properties.Model != "voice/pro" || mapped.TransactionID != event.TransactionID {
		t.Fatalf("bad mapping: %+v / %v", mapped, err)
	}
}

func TestFishAudioEventRejectsInvalidReports(t *testing.T) {
	p := &exporter{config: Config{CustomerMapping: map[string]string{"vk-1": "customer-1"}, DefaultCustomerID: "must-not-be-used"}}
	cases := []struct{ name, vk, id, body string }{
		{"unauthenticated", "", "req", audioBody},
		{"unmapped", "other", "req", audioBody},
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
			if _, err := p.fishAudioEvent(ctx, []byte(tc.body)); err == nil {
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
	p := &exporter{config: Config{CustomerMapping: map[string]string{"vk-1": "customer-1"}}, apiKey: "sandbox-test-key", ctx: context.Background()}
	p.client = &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Only tests reroute the production URL; configuration cannot redirect credentials.
		clone := r.Clone(r.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(clone)
	})}
	old := active
	active = p
	t.Cleanup(func() { active = old })
	req := &schemas.HTTPRequest{Method: "POST", Path: "/v1/fishaudio/usage", Body: []byte(audioBody)}
	for range 2 {
		resp := &schemas.HTTPResponse{StatusCode: 202}
		if err := HTTPTransportPostHook(audioContext("vk-1", "event-1"), req, resp); err != nil {
			t.Fatal(err)
		}
		if resp.Headers["x-bf-metronome-status"] != "sent" || resp.Headers["x-bf-metronome-transaction-id"] == "" {
			t.Fatalf("missing acknowledgment: %+v", resp)
		}
	}
	if len(received) != 3 || string(received[0]) != string(received[1]) || string(received[1]) != string(received[2]) {
		t.Fatalf("retry changed payload: %q", received)
	}
	var events []fishAudioEvent
	if err := json.Unmarshal(received[0], &events); err != nil || len(events) != 1 || events[0].Properties.BillableBytes != 54 {
		t.Fatalf("invalid wire event: %s", received[0])
	}
	for _, secret := range []string{"sandbox-test-key", "vk-1", "input_tokens"} {
		if strings.Contains(string(received[0]), secret) {
			t.Fatalf("unexpected field: %s", secret)
		}
	}
}

func TestFishAudioHTTPFailureAndDryRun(t *testing.T) {
	p := &exporter{config: Config{CustomerMapping: map[string]string{"vk-1": "customer-1"}}, ctx: context.Background()}
	old := active
	active = p
	t.Cleanup(func() { active = old })
	req := &schemas.HTTPRequest{Method: "POST", Path: "/v1/fishaudio/usage", Body: []byte(audioBody)}
	calls := 0
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("secret error body")), Header: make(http.Header)}, nil
	})}
	resp := &schemas.HTTPResponse{StatusCode: 202}
	err := HTTPTransportPostHook(audioContext("vk-1", "event"), req, resp)
	if err == nil || calls != 1 || len(resp.Headers) != 0 || strings.Contains(fmt.Sprint(err), "secret") {
		t.Fatalf("failure acknowledged: calls=%d resp=%+v err=%v", calls, resp, err)
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
	if err := HTTPTransportPostHook(ctx, req, resp); err == nil || calls != 1 {
		t.Fatal("cancelled request sent")
	}
	p.config.DryRun = true
	if err := HTTPTransportPostHook(audioContext("vk-1", "event"), req, resp); err != nil || calls != 1 || resp.Headers["x-bf-metronome-status"] != "dry_run" {
		t.Fatalf("dry run failed: %v", err)
	}
	p.closed = true
	if err := HTTPTransportPostHook(audioContext("vk-1", "event"), req, resp); err == nil {
		t.Fatal("closed exporter acknowledged")
	}
	// Other endpoints and governance rejection must never export usage.
	for _, test := range []struct {
		path   string
		status int
	}{{"/v1/audio/speech", 202}, {"/v1/fishaudio/usage", 403}} {
		if err := HTTPTransportPostHook(nil, &schemas.HTTPRequest{Method: "POST", Path: test.path}, &schemas.HTTPResponse{StatusCode: test.status}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFishAudioDeliveryRetryExhaustion(t *testing.T) {
	for _, status := range []int{0, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := &exporter{config: Config{CustomerMapping: map[string]string{"vk-1": "customer-1"}}, ctx: context.Background()}
			p.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if status == 0 {
					return nil, fmt.Errorf("test connection lost")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}
			old := active
			active = p
			t.Cleanup(func() { active = old })
			resp := &schemas.HTTPResponse{StatusCode: 202}
			err := HTTPTransportPostHook(audioContext("vk-1", "event"), &schemas.HTTPRequest{Method: "POST", Path: "/v1/fishaudio/usage", Body: []byte(audioBody)}, resp)
			if err == nil || calls != 3 || len(resp.Headers) != 0 {
				t.Fatalf("failed delivery acknowledged: calls=%d, err=%v", calls, err)
			}
		})
	}
}
