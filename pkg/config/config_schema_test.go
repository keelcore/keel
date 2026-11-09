// pkg/config/config_schema_test.go
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/knobs"
)

// ---------------------------------------------------------------------------
// newEngine
// ---------------------------------------------------------------------------

// newEngine attaches the embedded schema.yaml without error.
func TestNewEngine_AttachesEmbeddedSchema(t *testing.T) {
	if _, err := newEngine(); err != nil {
		t.Fatalf("newEngine: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Load — one config file, rooted at keel
// ---------------------------------------------------------------------------

// loadFile writes body to a config file and loads it.
func loadFile(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, "")
}

// Load accepts an empty config file.
func TestLoad_EmptyInput(t *testing.T) {
	if _, err := loadFile(t, ""); err != nil {
		t.Errorf("expected nil for empty input, got %v", err)
	}
}

// Load accepts a whitespace-only config file.
func TestLoad_WhitespaceOnly(t *testing.T) {
	if _, err := loadFile(t, "   \n  "); err != nil {
		t.Errorf("expected nil for whitespace-only input, got %v", err)
	}
}

// Load accepts a valid minimal document.
func TestLoad_ValidMinimalYAML(t *testing.T) {
	cfg, err := loadFile(t, "keel:\n  logging:\n    json: true\n")
	if err != nil || !cfg.Logging.JSON {
		t.Errorf("expected logging.json=true, got %v / %v", cfg.Logging.JSON, err)
	}
}

// Load rejects an unknown key.
func TestLoad_UnknownKey(t *testing.T) {
	if _, err := loadFile(t, "keel:\n  not_a_real_key: true\n"); err == nil {
		t.Error("expected error for unknown key, got nil")
	}
}

// Load rejects a document without the keel root.
func TestLoad_NoKeelRoot(t *testing.T) {
	if _, err := loadFile(t, "logging:\n  json: true\n"); err == nil {
		t.Error("expected error for a document not rooted at keel, got nil")
	}
}

// Load rejects a value that violates a constraint (a string port).
func TestLoad_TypeViolation(t *testing.T) {
	if _, err := loadFile(t, "keel:\n  listeners:\n    http:\n      port: \"not-a-number\"\n"); err == nil {
		t.Error("expected error for type violation, got nil")
	}
}

// Load accepts a YAML null document.
func TestLoad_NullDocument(t *testing.T) {
	if _, err := loadFile(t, "~\n"); err != nil {
		t.Errorf("expected nil for null YAML document, got %v", err)
	}
}

// Load rejects invalid (non-parseable) YAML.
func TestLoad_InvalidYAML(t *testing.T) {
	// Tabs at the start of a YAML mapping value are invalid in strict YAML.
	if _, err := loadFile(t, "key:\t value\n"); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

// Load rejects a document that is not a mapping.
func TestLoad_NotAMapping(t *testing.T) {
	if _, err := loadFile(t, "- logging\n"); err == nil {
		t.Error("expected error for a document that is not a mapping, got nil")
	}
}

// Load rejects a scalar where the schema has a nested object.
func TestLoad_ScalarForObject(t *testing.T) {
	_, err := loadFile(t, "keel:\n  listeners:\n    http: 8080\n")
	if err == nil || !strings.Contains(err.Error(), knobs.CodeInvalidType) {
		t.Errorf("expected %s error, got %v", knobs.CodeInvalidType, err)
	}
}

// ---------------------------------------------------------------------------
// Load — layers knobs resolves itself
// ---------------------------------------------------------------------------

// writeFile writes body to name in a temp dir with mode, and returns its path.
func writeFile(t *testing.T, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// With no path given, the global file comes from KEEL_KNOBS_GLOBAL_PATH.
func TestLoad_GlobalPathKnobFromEnv(t *testing.T) {
	t.Setenv("KEEL_KNOBS_GLOBAL_PATH", writeFile(t, "config.yaml", "keel:\n  logging:\n    level: debug\n", 0o600))
	cfg, err := Load("", "")
	if err != nil || cfg.Logging.Level != "debug" {
		t.Fatalf("Load = %q, %v; want level debug from the global file", cfg.Logging.Level, err)
	}
}

// A --knob argument overrides the same key from env.
func TestLoad_KnobArgumentOverridesEnv(t *testing.T) {
	t.Setenv("KEEL_LOGGING_LEVEL", "warn")
	SetKnobs([]string{"keel.logging.level=debug"})
	t.Cleanup(func() { SetKnobs(nil) })
	cfg, err := Load("", "")
	if err != nil || cfg.Logging.Level != "debug" {
		t.Fatalf("Load = %q, %v; want level debug from --knob", cfg.Logging.Level, err)
	}
}

// An owner-only secrets file is read, and overrides the config file.
func TestLoad_SecretsFile(t *testing.T) {
	cfgPath := writeFile(t, "config.yaml", "keel:\n  admin:\n    token: from-config\n", 0o600)
	secrets := writeFile(t, "secrets.yaml", "keel:\n  admin:\n    token: from-secrets\n", 0o600)
	cfg, err := Load(cfgPath, secrets)
	if err != nil || cfg.Admin.Token != "from-secrets" {
		t.Fatalf("Load = %q, %v; want the token from the secrets file", cfg.Admin.Token, err)
	}
}

// A secrets file others may read is refused.
func TestLoad_SecretsFileNotOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits carry no access rights on Windows; knobs checks the owner and ACL")
	}
	secrets := writeFile(t, "secrets.yaml", "keel:\n  admin:\n    token: s\n", 0o644)
	_, err := Load("", secrets)
	if err == nil || !strings.Contains(err.Error(), knobs.CodeSecretsFilePermissions) {
		t.Fatalf("Load = %v, want %s", err, knobs.CodeSecretsFilePermissions)
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
	if err := os.WriteFile(path, []byte("keel:\n  logging:\n    json: true\n"), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte("keel:\n  not_valid_key: true\n"), 0o600); err != nil {
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
// env — overrides read by keelcore/knobs, one variable per schema
// path (listeners.http.port is KEEL_LISTENERS_HTTP_PORT).
// ---------------------------------------------------------------------------

// Load sets each field from the variable named after its schema path.
func TestEnv_SchemaPathNames(t *testing.T) {
	t.Setenv("KEEL_LISTENERS_HTTP_PORT", "9999")
	t.Setenv("KEEL_SECURITY_MAX_REQUEST_BODY_BYTES", "1048576")
	t.Setenv("KEEL_BACKPRESSURE_HIGH_WATERMARK", "0.85")
	t.Setenv("KEEL_LOGGING_LEVEL", "debug")
	t.Setenv("KEEL_TIMEOUTS_READ", "5s")
	t.Setenv("KEEL_AUTHN_TRUSTED_IDS", "a, b , c")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listeners.HTTP.Port != 9999 || c.Security.MaxRequestBodyBytes != 1048576 ||
		c.Backpressure.HighWatermark != 0.85 || c.Logging.Level != "debug" ||
		c.Timeouts.Read.Duration != 5*time.Second || strings.Join(c.Authn.TrustedIDs, ",") != "a,b,c" {
		t.Fatalf("env not applied: %+v", c)
	}
}

// Load leaves a field alone when its variable is empty.
func TestEnv_EmptyIsUnset(t *testing.T) {
	t.Setenv("KEEL_LOGGING_LEVEL", "")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Logging.Level != Defaults().Logging.Level {
		t.Errorf("Level = %q, want the default", c.Logging.Level)
	}
}

// A flat pre-knobs name such as KEEL_HTTP_PORT names no schema path and has no effect.
func TestEnv_FlatNameHasNoEffect(t *testing.T) {
	t.Setenv("KEEL_HTTP_PORT", "9999")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listeners.HTTP.Port != Defaults().Listeners.HTTP.Port {
		t.Errorf("Port = %d, want the default", c.Listeners.HTTP.Port)
	}
}

// An unparseable value fails closed, naming the key it was meant for.
func TestEnv_Malformed_FailsClosed(t *testing.T) {
	for name, tc := range map[string]struct{ env, value, key string }{
		"bool":     {"KEEL_AUTHN_ENABLED", "flase", "keel.authn.enabled"},
		"int":      {"KEEL_LISTENERS_HTTP_PORT", "eighty", "keel.listeners.http.port"},
		"number":   {"KEEL_BACKPRESSURE_HIGH_WATERMARK", "high", "keel.backpressure.high_watermark"},
		"duration": {"KEEL_TIMEOUTS_SHUTDOWN_DRAIN", "soon", "shutdown_drain"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)
			_, err := load("", "")
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("load = %v, want an error naming %s", err, tc.key)
			}
		})
	}
}

// From fails closed on an unparseable env override.
func TestFrom_MalformedEnv_FailsClosed(t *testing.T) {
	t.Setenv("KEEL_LISTENERS_HTTP_PORT", "eighty")
	c := Defaults()
	if _, err := From(&c); err == nil {
		t.Fatal("expected From to fail on KEEL_LISTENERS_HTTP_PORT=eighty")
	}
}

// Load fails closed on an unparseable env override instead of keeping the default.
func TestLoad_MalformedEnv_FailsClosed(t *testing.T) {
	t.Setenv("KEEL_AUTHN_ENABLED", "flase")
	if _, err := Load("", ""); err == nil {
		t.Fatal("expected Load to fail on KEEL_AUTHN_ENABLED=flase")
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
