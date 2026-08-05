// pkg/config/config_coverage_test.go
package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"gopkg.in/yaml.v3"

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

// Default with no config/secrets env vars returns a validated config.
func TestDefault_Success(t *testing.T) {
	t.Setenv("KEEL_CONFIG", "")
	t.Setenv("KEEL_SECRETS", "")
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
	t.Setenv("KEEL_CONFIG", missing)
	t.Setenv("KEEL_SECRETS", "")
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
	if err := os.WriteFile(path, []byte("listeners:\n  https:\n    enabled: true\n"), 0o600); err != nil {
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
// load / applyYAMLFile — secrets-file and parse error branches.
// ---------------------------------------------------------------------------

// Load with a missing secrets file surfaces the "secrets file" error branch.
func TestLoad_SecretsFileError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-secrets.yaml")
	_, err := Load("", missing)
	if err == nil {
		t.Fatal("expected secrets-file error, got nil")
	}
	if !strings.Contains(err.Error(), "secrets file") {
		t.Errorf("expected secrets-file error, got %v", err)
	}
}

// applyYAMLFile with schema-valid YAML that fails strict decode (a duration
// field carrying a non-duration string) surfaces the parse error branch.
func TestApplyYAMLFile_ParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-duration.yaml")
	if err := os.WriteFile(path, []byte("timeouts:\n  read: \"notaduration\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaults()
	err := applyYAMLFile(path, &cfg)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// validateAgainstSchema — yaml.Unmarshal error branch.
// ---------------------------------------------------------------------------

// validateAgainstSchema with non-empty but unparseable YAML (unterminated flow
// sequence) fails at yaml.Unmarshal rather than at schema validation.
func TestValidateAgainstSchema_UnmarshalError(t *testing.T) {
	if err := validateAgainstSchema([]byte("[a, b")); err == nil {
		t.Error("expected unmarshal error for unterminated flow sequence, got nil")
	}
}

// ---------------------------------------------------------------------------
// getSchema — error branches.
//
// getSchema memoises via sync.Once, so these tests reset the compilation state
// (and restore a good schema afterwards) to drive each error path in turn.
// ---------------------------------------------------------------------------

func withFreshSchema(t *testing.T) {
	t.Helper()
	origYAML := SchemaYAML
	origMarshal := jsonMarshal
	origAdd := schemaAddResource
	reset := func() {
		compiledSchemaOnce = sync.Once{}
		compiledSchema = nil
		compiledSchemaErr = nil
	}
	t.Cleanup(func() {
		SchemaYAML = origYAML
		jsonMarshal = origMarshal
		schemaAddResource = origAdd
		reset()
		// Recompile the real schema so subsequent tests see a valid one.
		_, _ = getSchema()
	})
	reset()
}

// A malformed embedded schema fails at yaml.Unmarshal; validateAgainstSchema
// then surfaces the same getSchema error.
func TestGetSchema_BadEmbeddedSchema(t *testing.T) {
	withFreshSchema(t)
	SchemaYAML = []byte("[unterminated")
	if _, err := getSchema(); err == nil {
		t.Fatal("expected parse error for bad embedded schema, got nil")
	}
	// Drives the getSchema-error branch inside validateAgainstSchema (cached err).
	if err := validateAgainstSchema([]byte("logging:\n  json: true\n")); err == nil {
		t.Error("expected schema error from validateAgainstSchema, got nil")
	}
}

// A json.Marshal failure surfaces the "convert schema to JSON" error.
func TestGetSchema_JSONMarshalError(t *testing.T) {
	withFreshSchema(t)
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("boom") }
	_, err := getSchema()
	if err == nil || !strings.Contains(err.Error(), "convert schema to JSON") {
		t.Fatalf("expected convert-to-JSON error, got %v", err)
	}
}

// An AddResource failure surfaces the "load schema" error.
func TestGetSchema_AddResourceError(t *testing.T) {
	withFreshSchema(t)
	schemaAddResource = func(*jsonschema.Compiler, string, io.Reader) error {
		return errors.New("boom")
	}
	_, err := getSchema()
	if err == nil || !strings.Contains(err.Error(), "load schema") {
		t.Fatalf("expected load-schema error, got %v", err)
	}
}
