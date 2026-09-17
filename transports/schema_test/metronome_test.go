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
}
