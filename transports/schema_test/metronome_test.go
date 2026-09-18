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
	for _, config := range []string{`{"api_key":"literal-key"}`, `{"api_key":"env."}`, `{"api_key":"vault.some-key"}`, `{"customer_mapping":{"vk":"customer"}}`, `{"default_customer_id":"customer"}`} {
		if err := validateConfig(t, schema, `{"plugins":[{"name":"metronome","enabled":true,"config":`+config+`}]}`); err == nil {
			t.Fatalf("legacy customer routing accepted: %s", config)
		}
	}
	for _, config := range []string{`{"api_key":"env.METRONOME_API_KEY"}`, `{"customer_mapping":{},"default_customer_id":""}`, `{"customer_mapping":null,"default_customer_id":null}`} {
		if err := validateConfig(t, schema, `{"plugins":[{"name":"metronome","enabled":true,"config":`+config+`}]}`); err != nil {
			t.Fatalf("reference or empty legacy migration rejected: %v", err)
		}
	}

}
