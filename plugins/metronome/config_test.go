package metronome

import (
	"encoding/json"
	"testing"

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

func TestConfigCredentialAndIndependentInstances(t *testing.T) {
	t.Setenv("METRONOME_TEST_KEY", "test-secret")
	var cfg Config
	if err := json.Unmarshal([]byte(`{"api_key":"env.METRONOME_TEST_KEY"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.DryRun || cfg.APIKey.GetValue() != "test-secret" {
		t.Fatal("default or credential resolution failed")
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
	cfg.ModelMapping["fishaudio/s2-pro"] = "mutated"
	if err := first.Cleanup(); err != nil {
		t.Fatal(err)
	}
	receipt, err := second.ReportFishAudio(audioContext("vk-1", "event"), audioUsage(t, audioBody))
	if err != nil || receipt.Status != "dry_run" || second.config.ModelMapping["fishaudio/s2-pro"] != "fish-test-model" {
		t.Fatalf("reload/ownership failure: %v", err)
	}
	if _, err := Init(&Config{DryRun: false}, testLogger{}); err == nil {
		t.Fatal("live mode accepted missing key")
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
	key := redacted["api_key"]
	if key != "env.METRONOME_TEST_KEY" || raw["api_key"] != "env.METRONOME_TEST_KEY" {
		t.Fatal("credential exposed or source mutated")
	}
}
