// pkg/config/config_coverage_test.go
package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// Duration.UnmarshalYAML — all branches.
// ---------------------------------------------------------------------------

// A valid Go duration string unmarshals successfully.
func TestDurationUnmarshalYAML_Valid(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte("5s"), &d); err != nil {
		t.Fatalf("unmarshal valid duration: %v", err)
	}
	if d.Duration.Seconds() != 5 {
		t.Errorf("expected 5s, got %v", d.Duration)
	}
}

// A string that is not a valid Go duration returns a parse error.
func TestDurationUnmarshalYAML_BadDuration(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte("notaduration"), &d)
	if err == nil {
		t.Fatal("expected error for invalid duration string, got nil")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("expected invalid-duration error, got %v", err)
	}
}

// A non-scalar YAML node (sequence) fails the string decode step.
func TestDurationUnmarshalYAML_NonString(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte("[1, 2, 3]"), &d)
	if err == nil {
		t.Fatal("expected error decoding sequence into duration, got nil")
	}
	if !strings.Contains(err.Error(), "duration must be a string") {
		t.Errorf("expected must-be-a-string error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Default — success path and Fatal path (via injectable ExitFn).
// ---------------------------------------------------------------------------

// Default with no config/secrets path knobs returns a validated config.
func TestDefault_Success(t *testing.T) {
	t.Setenv("KEEL_KNOBS_GLOBAL_PATH", "")
	t.Setenv("KEEL_KNOBS_SECRETS_PATH", "")
	exited := false
	log := logging.New(logging.Config{Out: io.Discard})
	log.ExitFn = func(int) { exited = true }
	cfg := Default(log)
	if exited {
		t.Error("ExitFn should not be called on success")
	}
	if cfg.Listeners.HTTP.Port == 0 {
		t.Error("expected populated defaults")
	}
}

// Default with a bad config path triggers the Fatal branch (ExitFn invoked).
func TestDefault_FatalOnLoadError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	t.Setenv("KEEL_KNOBS_GLOBAL_PATH", missing)
	t.Setenv("KEEL_KNOBS_SECRETS_PATH", "")
	exitCode := -1
	log := logging.New(logging.Config{Out: io.Discard})
	log.ExitFn = func(code int) { exitCode = code }
	_ = Default(log)
	if exitCode != 1 {
		t.Errorf("expected Fatal ExitFn(1), got exit code %d", exitCode)
	}
}

// ---------------------------------------------------------------------------
// Load / From — validation-failure branches.
// ---------------------------------------------------------------------------

// Load with a schema-valid file that fails Validate returns the validation error.
func TestLoad_ValidateError(t *testing.T) {
	// HTTPS enabled, no cert, ACME disabled → Validate rejects.
	path := filepath.Join(t.TempDir(), "keel.yaml")
	if err := os.WriteFile(path, []byte("keel:\n  listeners:\n    https:\n      enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, "")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "no TLS cert") {
		t.Errorf("expected TLS cert validation error, got %v", err)
	}
}

// From with a config that fails Validate returns the validation error.
func TestFrom_ValidateError(t *testing.T) {
	c := Defaults()
	c.Listeners.HTTPS.Enabled = true // no cert, ACME off → invalid
	_, err := From(&c)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// ---------------------------------------------------------------------------
// load — secrets-file and decode error branches.
// ---------------------------------------------------------------------------

// Load with a missing secrets file fails in the secrets layer.
func TestLoad_SecretsFileError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-secrets.yaml")
	_, err := Load("", missing)
	if err == nil {
		t.Fatal("expected secrets-file error, got nil")
	}
	if !strings.Contains(err.Error(), "(secrets)") {
		t.Errorf("expected secrets-file error, got %v", err)
	}
}

// A schema-valid value that does not decode into its field (a duration field
// carrying a non-duration string) fails, naming the key.
func TestLoad_DecodeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-duration.yaml")
	if err := os.WriteFile(path, []byte("keel:\n  timeouts:\n    read: \"notaduration\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, "")
	if err == nil || !strings.Contains(err.Error(), "keel.timeouts.read") {
		t.Fatalf("expected a decode error naming keel.timeouts.read, got %v", err)
	}
}

// Non-empty but unparseable YAML (an unterminated flow sequence) fails.
func TestLoad_UnmarshalError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("[a, b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Error("expected unmarshal error for unterminated flow sequence, got nil")
	}
}

// An unparseable --knob argument fails before any layer resolves.
func TestLoad_MalformedKnobArgument(t *testing.T) {
	SetKnobs([]string{"keel.listeners.http.port"})
	t.Cleanup(func() { SetKnobs(nil) })
	if _, err := Load("", ""); err == nil {
		t.Fatal("expected error for a --knob without =, got nil")
	}
}

// ---------------------------------------------------------------------------
// newEngine — a bad embedded schema.
// ---------------------------------------------------------------------------

// withBadSchema replaces the embedded schema for one test.
func withBadSchema(t *testing.T) {
	t.Helper()
	origYAML := SchemaYAML
	t.Cleanup(func() { SchemaYAML = origYAML })
	SchemaYAML = []byte("[unterminated")
}

// A bad embedded schema fails Load before any layer resolves.
func TestLoad_BadEmbeddedSchema(t *testing.T) {
	withBadSchema(t)
	if _, err := Load("", ""); err == nil || !strings.Contains(err.Error(), "load schema") {
		t.Fatalf("expected a load-schema error, got %v", err)
	}
}

// A bad embedded schema fails From before any override is read.
func TestFrom_BadEmbeddedSchema(t *testing.T) {
	withBadSchema(t)
	c := Defaults()
	if _, err := From(&c); err == nil {
		t.Fatal("expected schema error from From, got nil")
	}
}
