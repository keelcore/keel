// pkg/apps/myapp/config_test.go
package myapp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// newTestLogger returns a Logger whose ExitFn is a no-op (so Fatal/Exit do not
// terminate the test process) plus a pointer to a counter of exit invocations.
func newTestLogger(t *testing.T) (*logging.Logger, *int) {
	t.Helper()
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	exits := 0
	log.ExitFn = func(int) { exits++ }
	return log, &exits
}

// writeTempFile writes content to a temp file and returns its path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

func TestLoadConfig_HappyPath(t *testing.T) {
	cfgPath := writeTempFile(t, "config.yaml", "app:\n  name: myapp-test\n")
	// APP_CONFIG set (non-empty path branch), APP_SECRETS empty (early-return branch).
	t.Setenv("APP_CONFIG", cfgPath)
	t.Setenv("APP_SECRETS", "")

	log, exits := newTestLogger(t)
	cfg := loadConfig(log)

	if *exits != 0 {
		t.Fatalf("expected no fatal exit, got %d", *exits)
	}
	if cfg.App.Name != "myapp-test" {
		t.Errorf("expected app name myapp-test, got %q", cfg.App.Name)
	}
}

func TestLoadConfig_ReadError(t *testing.T) {
	// Non-existent path exercises the os.ReadFile error branch in applyYAML.
	t.Setenv("APP_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	t.Setenv("APP_SECRETS", "")

	log, exits := newTestLogger(t)
	_ = loadConfig(log)

	if *exits == 0 {
		t.Fatalf("expected fatal exit on read error")
	}
}

func TestLoadConfig_ParseError(t *testing.T) {
	// Malformed YAML exercises the yaml.Unmarshal error branch in applyYAML.
	badPath := writeTempFile(t, "bad.yaml", "app: [unterminated\n")
	t.Setenv("APP_CONFIG", badPath)
	t.Setenv("APP_SECRETS", "")

	log, exits := newTestLogger(t)
	_ = loadConfig(log)

	if *exits == 0 {
		t.Fatalf("expected fatal exit on parse error")
	}
}

func TestLoadConfig_ValidateError(t *testing.T) {
	// Enabling HTTPS with no cert and ACME disabled makes keelconfig.From fail,
	// exercising the error branch in loadConfig.
	cfgPath := writeTempFile(t, "invalid.yaml",
		"keel:\n  listeners:\n    https:\n      enabled: true\n")
	t.Setenv("APP_CONFIG", cfgPath)
	t.Setenv("APP_SECRETS", "")

	log, exits := newTestLogger(t)
	_ = loadConfig(log)

	if *exits == 0 {
		t.Fatalf("expected fatal exit on config validation error")
	}
}
