// pkg/config/config_fuzz_test.go
package config

// Fuzz target for config parsing: keel.yaml and keel-secrets.yaml go through
// schema validation and strict YAML decoding before any field is trusted.

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzLoadFile(f *testing.F) {
	f.Add([]byte("keel:\n  logging:\n    level: debug\n"))
	f.Add([]byte("keel:\n  listeners:\n    http:\n      enabled: true\n      port: 8080\n"))
	f.Add([]byte("unknown_field: 1\n"))
	f.Add([]byte("a: &a [*a]\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "keel.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = load(path, "")
	})
}
