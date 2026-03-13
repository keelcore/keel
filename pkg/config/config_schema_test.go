// pkg/config/config_schema_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// getSchema
// ---------------------------------------------------------------------------

// getSchema returns a non-nil schema and no error for the embedded schema.yaml.
func TestGetSchema_ReturnsSchemaNonNil(t *testing.T) {
	sc, err := getSchema()
	if err != nil {
		t.Fatalf("getSchema: %v", err)
	}
	if sc == nil {
		t.Error("expected non-nil schema")
	}
}

// getSchema is idempotent (sync.Once): calling it twice returns the same result.
func TestGetSchema_Idempotent(t *testing.T) {
	sc1, err1 := getSchema()
	sc2, err2 := getSchema()
	if err1 != nil || err2 != nil {
		t.Fatalf("getSchema errors: %v / %v", err1, err2)
	}
	if sc1 != sc2 {
		t.Error("expected same schema pointer on second call (sync.Once)")
	}
}

// ---------------------------------------------------------------------------
// validateAgainstSchema
// ---------------------------------------------------------------------------

// validateAgainstSchema accepts empty input.
func TestValidateAgainstSchema_EmptyInput(t *testing.T) {
	if err := validateAgainstSchema([]byte{}); err != nil {
		t.Errorf("expected nil for empty input, got %v", err)
	}
}

// validateAgainstSchema accepts whitespace-only input.
func TestValidateAgainstSchema_WhitespaceOnly(t *testing.T) {
	if err := validateAgainstSchema([]byte("   \n  ")); err != nil {
		t.Errorf("expected nil for whitespace-only input, got %v", err)
	}
}

// validateAgainstSchema accepts a valid minimal YAML document.
func TestValidateAgainstSchema_ValidMinimalYAML(t *testing.T) {
	yaml := []byte("logging:\n  json: true\n")
	if err := validateAgainstSchema(yaml); err != nil {
		t.Errorf("expected nil for valid YAML, got %v", err)
	}
}

// validateAgainstSchema rejects YAML with unknown top-level keys.
func TestValidateAgainstSchema_UnknownKey(t *testing.T) {
	yaml := []byte("not_a_real_key: true\n")
	if err := validateAgainstSchema(yaml); err == nil {
		t.Error("expected error for unknown key, got nil")
	}
}

// validateAgainstSchema rejects YAML with a value that violates a constraint
// (e.g. a port value that is a string instead of integer).
func TestValidateAgainstSchema_TypeViolation(t *testing.T) {
	yaml := []byte("listeners:\n  http:\n    port: \"not-a-number\"\n")
	if err := validateAgainstSchema(yaml); err == nil {
		t.Error("expected error for type violation, got nil")
	}
}

// validateAgainstSchema with a YAML null document (only "~" or "null") returns nil.
func TestValidateAgainstSchema_NullDocument(t *testing.T) {
	// A YAML null document unmarshals to nil, which is accepted.
	if err := validateAgainstSchema([]byte("~\n")); err != nil {
		t.Errorf("expected nil for null YAML document, got %v", err)
	}
}

// validateAgainstSchema with invalid (non-parseable) YAML returns an error.
func TestValidateAgainstSchema_InvalidYAML(t *testing.T) {
	// Tabs at the start of a YAML mapping value are invalid in strict YAML.
	invalid := []byte("key:\t value\n")
	if err := validateAgainstSchema(invalid); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

// Load with a missing config file returns an error.
func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	_, err := Load("/nonexistent-config-for-test.yaml", "")
	if err == nil {
		t.Error("expected error for missing config file, got nil")
	}
}

// Load with a valid minimal config file returns a non-zero config.
func TestLoad_ValidFile_ReturnsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(path, []byte("logging:\n  json: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Logging.JSON {
		t.Error("expected logging.json=true from loaded config")
	}
}

// Load with an invalid YAML config file returns an error.
func TestLoad_InvalidYAML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(path, []byte("not_valid_key: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, "")
	if err == nil {
		t.Error("expected error for invalid/unknown YAML key, got nil")
	}
}

// ---------------------------------------------------------------------------
// From
// ---------------------------------------------------------------------------

// From with a valid Config returns it unchanged (no env overrides set).
func TestFrom_ValidConfig_ReturnsConfig(t *testing.T) {
	cfg := Defaults()
	got, err := From(&cfg)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if got.Listeners.HTTP.Port != cfg.Listeners.HTTP.Port {
		t.Errorf("expected same HTTP port, got %d", got.Listeners.HTTP.Port)
	}
}

// ---------------------------------------------------------------------------
// Validate edge cases
// ---------------------------------------------------------------------------

// Validate returns an error when backpressure.low_watermark >= high_watermark.
func TestValidate_LowWatermarkTooHigh(t *testing.T) {
	cfg := Defaults()
	cfg.Backpressure.HighWatermark = 0.70
	cfg.Backpressure.LowWatermark = 0.80
	if err := Validate(cfg); err == nil {
		t.Error("expected error when low_watermark >= high_watermark")
	}
}

// Validate returns an error when sidecar is enabled but upstream_url is empty.
func TestValidate_SidecarNoUpstreamURL(t *testing.T) {
	cfg := Defaults()
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = ""
	if err := Validate(cfg); err == nil {
		t.Error("expected error when sidecar enabled but upstream_url empty")
	}
}

// Validate returns an error when HTTPS enabled but no cert/key and ACME disabled.
func TestValidate_HTTPSNoCert_ReturnsError(t *testing.T) {
	cfg := Defaults()
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.CertFile = ""
	cfg.TLS.KeyFile = ""
	cfg.TLS.ACME.Enabled = false
	if err := Validate(cfg); err == nil {
		t.Error("expected error when HTTPS enabled with no TLS cert and ACME disabled")
	}
}

// Validate returns an error when ACME enabled but cert_file is also set.
func TestValidate_ACMEAndCertBothSet_ReturnsError(t *testing.T) {
	cfg := Defaults()
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.com"}
	cfg.TLS.CertFile = "/some/cert.pem"
	cfg.TLS.KeyFile = "/some/key.pem"
	if err := Validate(cfg); err == nil {
		t.Error("expected error when ACME enabled and cert_file/key_file also set")
	}
}

// Validate returns an error when ACME enabled but no domains configured.
func TestValidate_ACMENoDomains_ReturnsError(t *testing.T) {
	cfg := Defaults()
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = nil
	if err := Validate(cfg); err == nil {
		t.Error("expected error when ACME enabled but domains is empty")
	}
}

// Validate returns an error when ACME challenge_port is not 80 (RFC 8555 §8.3).
func TestValidate_ACMEChallengePortNot80(t *testing.T) {
	cfg := Config{}
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.com"}
	cfg.TLS.ACME.ChallengePort = 8080 // non-80; triggers line 370 in config.go
	if err := Validate(cfg); err == nil {
		t.Error("expected error when ACME challenge_port is not 80")
	}
}

// Load with a 0-byte config file returns defaults (io.EOF path in config.go:397).
func TestLoad_EmptyYAMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, "")
	if err != nil {
		t.Errorf("expected nil error for 0-byte config file, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// applyEnv — env var override functions
// ---------------------------------------------------------------------------

// applyBool sets the field when the env var is a valid bool string.
func TestApplyBool_ValidBool(t *testing.T) {
	t.Setenv("KEEL_TEST_BOOL_APPLY", "true")
	var v bool
	applyBool("KEEL_TEST_BOOL_APPLY", &v)
	if !v {
		t.Error("expected applyBool to set true")
	}
}

// applyBool ignores invalid bool strings.
func TestApplyBool_InvalidBool_NoChange(t *testing.T) {
	t.Setenv("KEEL_TEST_BOOL_INVALID", "not-a-bool")
	v := true
	applyBool("KEEL_TEST_BOOL_INVALID", &v)
	if !v {
		t.Error("expected applyBool to leave value unchanged for invalid input")
	}
}

// applyInt sets the field when the env var is a valid integer string.
func TestApplyInt_ValidInt(t *testing.T) {
	t.Setenv("KEEL_TEST_INT_APPLY", "9999")
	var v int
	applyInt("KEEL_TEST_INT_APPLY", &v)
	if v != 9999 {
		t.Errorf("expected 9999, got %d", v)
	}
}

// applyInt64 sets the field from env var.
func TestApplyInt64_ValidInt64(t *testing.T) {
	t.Setenv("KEEL_TEST_INT64_APPLY", "1048576")
	var v int64
	applyInt64("KEEL_TEST_INT64_APPLY", &v)
	if v != 1048576 {
		t.Errorf("expected 1048576, got %d", v)
	}
}

// applyFloat64 sets the field from env var.
func TestApplyFloat64_ValidFloat(t *testing.T) {
	t.Setenv("KEEL_TEST_FLOAT_APPLY", "0.85")
	var v float64
	applyFloat64("KEEL_TEST_FLOAT_APPLY", &v)
	if v != 0.85 {
		t.Errorf("expected 0.85, got %f", v)
	}
}

// applyString sets the field when the env var is non-empty.
func TestApplyString_NonEmpty(t *testing.T) {
	t.Setenv("KEEL_TEST_STRING_APPLY", "hello")
	var v string
	applyString("KEEL_TEST_STRING_APPLY", &v)
	if v != "hello" {
		t.Errorf("expected 'hello', got %q", v)
	}
}

// applyString does nothing when the env var is empty.
func TestApplyString_Empty_NoChange(t *testing.T) {
	t.Setenv("KEEL_TEST_STRING_EMPTY", "")
	v := "original"
	applyString("KEEL_TEST_STRING_EMPTY", &v)
	if v != "original" {
		t.Errorf("expected 'original' unchanged, got %q", v)
	}
}

// applyDuration sets the field from env var.
func TestApplyDuration_ValidDuration(t *testing.T) {
	t.Setenv("KEEL_TEST_DUR_APPLY", "5s")
	var v Duration
	applyDuration("KEEL_TEST_DUR_APPLY", &v)
	if v.Duration.Seconds() != 5 {
		t.Errorf("expected 5s, got %v", v)
	}
}

// applyDuration ignores invalid duration strings.
func TestApplyDuration_InvalidDuration_NoChange(t *testing.T) {
	t.Setenv("KEEL_TEST_DUR_INVALID", "not-a-duration")
	v := DurationOf(0)
	applyDuration("KEEL_TEST_DUR_INVALID", &v)
	if v.Duration != 0 {
		t.Errorf("expected zero duration unchanged, got %v", v)
	}
}

// applyCSV splits comma-separated values and sets the slice.
func TestApplyCSV_ValidCSV(t *testing.T) {
	t.Setenv("KEEL_TEST_CSV_APPLY", "a, b , c")
	var v []string
	applyCSV("KEEL_TEST_CSV_APPLY", &v)
	if len(v) != 3 {
		t.Errorf("expected 3 values, got %d: %v", len(v), v)
	}
}

// applyCSV does nothing when env var is empty.
func TestApplyCSV_Empty_NoChange(t *testing.T) {
	t.Setenv("KEEL_TEST_CSV_EMPTY", "")
	v := []string{"existing"}
	applyCSV("KEEL_TEST_CSV_EMPTY", &v)
	if len(v) != 1 || v[0] != "existing" {
		t.Errorf("expected unchanged slice, got %v", v)
	}
}

// ---------------------------------------------------------------------------
// AddrFromPort
// ---------------------------------------------------------------------------

func TestAddrFromPort_ReturnsColonPort(t *testing.T) {
	got := AddrFromPort(8080)
	if got != ":8080" {
		t.Errorf("expected ':8080', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

// Defaults returns a Config with non-zero HTTP port.
func TestDefaults_NonZeroHTTPPort(t *testing.T) {
	cfg := Defaults()
	if cfg.Listeners.HTTP.Port == 0 {
		t.Error("expected non-zero HTTP port in Defaults")
	}
}

// ---------------------------------------------------------------------------
// DurationOf
// ---------------------------------------------------------------------------

func TestDurationOf_RoundTrip(t *testing.T) {
	d := DurationOf(10 * time.Second)
	if d.Duration != 10*time.Second {
		t.Errorf("DurationOf: expected 10s, got %v", d.Duration)
	}
}
