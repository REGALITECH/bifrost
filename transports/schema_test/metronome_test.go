package schema_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaMetronomeBuiltin(t *testing.T) {
	schema := compileSchema(t)
	example := filepath.Join(filepath.Dir(getSchemaPath(t)), "..", "examples", "configs", "withmetronome", "config.json")
	data, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConfig(t, schema, string(data)); err != nil {
		t.Fatalf("builtin example rejected: %v", err)
	}
	if err := validateConfig(t, schema, `{"plugins":[{"name":"metronome","enabled":true}]}`); err == nil {
		t.Fatal("missing builtin config accepted")
	}
	for _, config := range []string{`{"customer_mapping":{"vk":"customer"}}`, `{"default_customer_id":"customer"}`} {
		if err := validateConfig(t, schema, `{"plugins":[{"name":"metronome","enabled":true,"config":`+config+`}]}`); err == nil {
			t.Fatalf("legacy customer routing accepted: %s", config)
		}
	}
	for _, tc := range []struct {
		name    string
		plugin  string
		wantErr bool
	}{
		{"enabled environment key", `{"name":"metronome","enabled":true,"config":{"api_key":"env.METRONOME_API_KEY"}}`, false},
		{"enabled missing key", `{"name":"metronome","enabled":true,"config":{}}`, true},
		{"enabled literal key", `{"name":"metronome","enabled":true,"config":{"api_key":"literal-test-key"}}`, true},
		{"enabled vault key", `{"name":"metronome","enabled":true,"config":{"api_key":"vault.test/key"}}`, true},
		{"empty environment reference", `{"name":"metronome","enabled":true,"config":{"api_key":"env."}}`, true},
		{"disabled missing key", `{"name":"metronome","enabled":false,"config":{}}`, false},
		{"disabled environment key", `{"name":"metronome","enabled":false,"config":{"api_key":"env.METRONOME_API_KEY"}}`, false},
		{"disabled literal key", `{"name":"metronome","enabled":false,"config":{"api_key":"literal-test-key"}}`, true},
		{"legacy dry run", `{"name":"metronome","enabled":true,"config":{"api_key":"env.METRONOME_API_KEY","dry_run":true}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(t, schema, `{"plugins":[`+tc.plugin+`]}`)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validation error = %v, wantErr = %t", err, tc.wantErr)
			}
		})
	}
}
