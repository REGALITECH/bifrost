package metronome

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type testLogger struct{}

func (testLogger) Debug(string, ...any)                   {}
func (testLogger) Info(string, ...any)                    {}
func (testLogger) Warn(string, ...any)                    {}
func (testLogger) Error(string, ...any)                   {}
func (testLogger) Fatal(string, ...any)                   {}
func (testLogger) SetLevel(schemas.LogLevel)              {}
func (testLogger) SetOutputType(schemas.LoggerOutputType) {}
func (testLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func TestInitRequiresEnvironmentKey(t *testing.T) {
	t.Setenv("METRONOME_TEST_KEY", "")
	var omitted Config
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []*Config{nil, &omitted, {APIKey: schemas.NewSecretVar("env.METRONOME_TEST_KEY")}, {APIKey: schemas.NewSecretVar("test-literal-secret")}} {
		p, err := Init(cfg, testLogger{})
		if err == nil {
			_ = p.Cleanup()
			t.Fatal("initialization accepted missing environment credential or a literal key")
		}
		if strings.Contains(err.Error(), "test-literal-secret") {
			t.Fatal("credential appeared in initialization error")
		}
	}
}

func TestDeliverTokenAndFishAudio(t *testing.T) {
	t.Setenv("METRONOME_TEST_KEY", "test-memory-only-key")
	var cfg Config
	if err := json.Unmarshal([]byte(`{"api_key":"env.METRONOME_TEST_KEY"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	p, err := Init(&cfg, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Cleanup() })
	sent := make(chan string, 2)
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-memory-only-key" {
			t.Error("missing environment credential on delivery")
		}
		body, _ := io.ReadAll(r.Body)
		sent <- string(body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	ctx := audioContext("vk-1", "event")
	_, _, err = p.PreLLMHook(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.PostLLMHook(ctx, tokenIdentityResponse(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-sent:
		if !strings.Contains(body, `"event_type":"token-billing"`) {
			t.Fatal("token event was not sent")
		}
	case <-time.After(time.Second):
		t.Fatal("token event was not sent")
	}
	receipt, err := p.ReportFishAudio(ctx, audioUsage(t, audioBody))
	if err != nil || receipt.Status != "sent" {
		t.Fatalf("Fish Audio delivery was not acknowledged: receipt=%+v err=%v", receipt, err)
	}
	select {
	case body := <-sent:
		if !strings.Contains(body, `"event_type":"fishaudio-usage"`) {
			t.Fatal("Fish Audio event was not sent")
		}
	default:
		t.Fatal("Fish Audio was acknowledged without sending")
	}
}

func TestConfigCredentialAndIndependentInstances(t *testing.T) {
	t.Setenv("METRONOME_TEST_KEY", "test-secret")
	var cfg Config
	if err := json.Unmarshal([]byte(`{"api_key":"env.METRONOME_TEST_KEY"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey.GetValue() != "test-secret" {
		t.Fatal("credential resolution failed")
	}
	cfg.ModelMapping = map[string]string{"fishaudio/s2-pro": "fish-test-model"}
	first, err := Init(&cfg, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cleanup()
	second, err := Init(&cfg, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cleanup()
	second.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	cfg.ModelMapping["fishaudio/s2-pro"] = "mutated"
	if err := first.Cleanup(); err != nil {
		t.Fatal(err)
	}
	receipt, err := second.ReportFishAudio(audioContext("vk-1", "event"), audioUsage(t, audioBody))
	if err != nil || receipt.Status != "sent" || second.config.ModelMapping["fishaudio/s2-pro"] != "fish-test-model" {
		t.Fatalf("reload/ownership failure: %v", err)
	}
	if _, err := Init(&Config{}, testLogger{}); err == nil {
		t.Fatal("initialization accepted missing key")
	}
	raw := map[string]any{"api_key": "env.METRONOME_TEST_KEY"}
	stored, err := second.MarshalConfigForStorage(raw)
	if err != nil || stored["api_key"] != "env.METRONOME_TEST_KEY" {
		t.Fatalf("lost credential reference: %v", err)
	}
	redacted, err := second.RedactConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	key := redacted["api_key"].(*schemas.SecretVar)
	if key.GetValue() == "test-secret" || raw["api_key"] != "env.METRONOME_TEST_KEY" {
		t.Fatal("credential exposed or source mutated")
	}
}
