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
