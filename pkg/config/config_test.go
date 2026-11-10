package config

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/knobs"
	"go.yaml.in/yaml/v3"
)

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

// newEngine attaches the embedded schema.yaml without error.
func TestNewEngine_AttachesEmbeddedSchema(t *testing.T) {
	if _, err := newEngine(); err != nil {
		t.Fatalf("newEngine: %v", err)
	}
}

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

func TestAddrFromPort_ReturnsColonPort(t *testing.T) {
	got := AddrFromPort(8080)
	if got != ":8080" {
		t.Errorf("expected ':8080', got %q", got)
	}
}

// Defaults returns a Config with non-zero HTTP port.
func TestDefaults_NonZeroHTTPPort(t *testing.T) {
	cfg := Defaults()
	if cfg.Listeners.HTTP.Port == 0 {
		t.Error("expected non-zero HTTP port in Defaults")
	}
}

func TestDurationOf_RoundTrip(t *testing.T) {
	d := DurationOf(10 * time.Second)
	if d.Duration != 10*time.Second {
		t.Errorf("DurationOf: expected 10s, got %v", d.Duration)
	}
}

func TestAudit_DefaultsAreOffAndValid(t *testing.T) {
	c := Defaults()
	if c.Audit.Enabled || c.Audit.File != "" {
		t.Fatalf("audit defaults = %+v", c.Audit)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_Audit(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*AuditConfig)
		want string
	}{
		{"enabled with a file", func(a *AuditConfig) { a.Enabled = true; a.File = "/a.log" }, ""},
		{"enabled with a remote sink", func(a *AuditConfig) {
			a.Enabled = true
			a.RemoteSink = RemoteSinkConfig{Enabled: true, Endpoint: "https://logs", Protocol: "http"}
		}, ""},
		{"enabled with nowhere to write", func(a *AuditConfig) { a.Enabled = true }, "audit"},
		{"negative rotation", func(a *AuditConfig) { a.Rotation.MaxBackups = -1 }, "max_backups"},
	} {
		c := Defaults()
		tc.edit(&c.Audit)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_Audit(t *testing.T) {
	t.Setenv("KEEL_AUDIT_ENABLED", "true")
	t.Setenv("KEEL_AUDIT_FILE", "/a.log")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Audit.Enabled || c.Audit.File != "/a.log" {
		t.Fatalf("env not applied: %+v", c.Audit)
	}
}

// ext_authz with no endpoint or a non-positive timeout would fail every call,
// which fail_open turns into allow-all; Validate rejects both.
func TestValidate_ExtAuthzRequiresEndpointAndTimeout(t *testing.T) {
	base := Defaults()
	base.ExtAuthz.Enabled = true
	base.ExtAuthz.Endpoint = "http://127.0.0.1:8181/v1/data/authz/allow"
	base.ExtAuthz.Timeout = DurationOf(500 * time.Millisecond)
	if err := Validate(base); err != nil {
		t.Fatalf("valid ext_authz config rejected: %v", err)
	}

	noEndpoint := base
	noEndpoint.ExtAuthz.Endpoint = ""
	if err := Validate(noEndpoint); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("empty endpoint: err = %v", err)
	}

	zeroTimeout := base
	zeroTimeout.ExtAuthz.Timeout = DurationOf(0)
	if err := Validate(zeroTimeout); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("zero timeout: err = %v", err)
	}
}

func TestLogging_FileAndRotationDefaults(t *testing.T) {
	c := Defaults()
	if c.Logging.File != "" {
		t.Fatalf("logging.file = %q, want off by default", c.Logging.File)
	}
	if r := c.Logging.Rotation; r.MaxSizeMB != 100 || r.MaxAgeDays != 30 || r.MaxBackups != 5 || !r.Compress {
		t.Fatalf("rotation defaults = %+v", r)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_LogRotation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*LogRotationConfig)
		want string
	}{
		{"zero size is allowed", func(r *LogRotationConfig) { r.MaxSizeMB = 0 }, ""},
		{"negative size", func(r *LogRotationConfig) { r.MaxSizeMB = -1 }, "max_size_mb"},
		{"negative age", func(r *LogRotationConfig) { r.MaxAgeDays = -1 }, "max_age_days"},
		{"negative backups", func(r *LogRotationConfig) { r.MaxBackups = -1 }, "max_backups"},
	} {
		c := Defaults()
		tc.edit(&c.Logging.Rotation)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_LogRotation(t *testing.T) {
	t.Setenv("KEEL_LOGGING_FILE", "/var/log/keel.log")
	t.Setenv("KEEL_LOGGING_ROTATION_MAX_SIZE_MB", "7")
	t.Setenv("KEEL_LOGGING_ROTATION_MAX_AGE_DAYS", "8")
	t.Setenv("KEEL_LOGGING_ROTATION_MAX_BACKUPS", "9")
	t.Setenv("KEEL_LOGGING_ROTATION_COMPRESS", "false")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	r := c.Logging.Rotation
	if c.Logging.File != "/var/log/keel.log" || r.MaxSizeMB != 7 || r.MaxAgeDays != 8 || r.MaxBackups != 9 || r.Compress {
		t.Fatalf("env not applied: %q %+v", c.Logging.File, r)
	}
}

func TestOIDC_DefaultsAreOffAndValid(t *testing.T) {
	c := Defaults()
	o := c.Authn.OIDC
	if o.Enabled || o.SessionTTL.Duration != 8*time.Hour || len(o.Scopes) != 3 {
		t.Fatalf("defaults = %+v", o)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_OIDC(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*OIDCConfig)
		want string
	}{
		{"complete", func(*OIDCConfig) {}, ""},
		{"loopback http for development", func(o *OIDCConfig) {
			o.Issuer, o.RedirectURL = "http://127.0.0.1:5556", "http://localhost:8080/oauth2/callback"
		}, ""},
		{"no issuer", func(o *OIDCConfig) { o.Issuer = "" }, "issuer"},
		{"plain http issuer", func(o *OIDCConfig) { o.Issuer = "http://idp.example" }, "issuer"},
		{"no client", func(o *OIDCConfig) { o.ClientID = "" }, "client_id"},
		{"no redirect", func(o *OIDCConfig) { o.RedirectURL = "" }, "redirect_url"},
		{"plain http redirect", func(o *OIDCConfig) { o.RedirectURL = "http://app.example/cb" }, "redirect_url"},
		{"redirect at the root", func(o *OIDCConfig) { o.RedirectURL = "https://app.example/" }, "redirect_url"},
		{"no cookie key", func(o *OIDCConfig) { o.CookieKeyFile = "" }, "cookie_key_file"},
		{"no openid scope", func(o *OIDCConfig) { o.Scopes = []string{"email"} }, "openid"},
		{"negative ttl", func(o *OIDCConfig) { o.SessionTTL = DurationOf(-time.Second) }, "session_ttl"},
	} {
		c := Defaults()
		scopes, ttl := c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL
		c.Authn.OIDC = validOIDC()
		c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL = scopes, ttl
		tc.edit(&c.Authn.OIDC)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_OIDC(t *testing.T) {
	t.Setenv("KEEL_AUTHN_OIDC_ENABLED", "true")
	t.Setenv("KEEL_AUTHN_OIDC_ISSUER", "https://idp")
	t.Setenv("KEEL_AUTHN_OIDC_CLIENT_ID", "id")
	t.Setenv("KEEL_AUTHN_OIDC_CLIENT_SECRET", "s")
	t.Setenv("KEEL_AUTHN_OIDC_REDIRECT_URL", "https://a/cb")
	t.Setenv("KEEL_AUTHN_OIDC_COOKIE_KEY_FILE", "/k")
	t.Setenv("KEEL_AUTHN_OIDC_CA_FILE", "/ca")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	o := c.Authn.OIDC
	if !o.Enabled || o.Issuer != "https://idp" || o.ClientID != "id" || o.ClientSecret != "s" ||
		o.RedirectURL != "https://a/cb" || o.CookieKeyFile != "/k" || o.CAFile != "/ca" {
		t.Fatalf("env not applied: %+v", o)
	}
}

func TestValidate_OIDCRejectsOtherSchemes(t *testing.T) {
	c := Defaults()
	scopes, ttl := c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL
	c.Authn.OIDC = validOIDC()
	c.Authn.OIDC.Scopes, c.Authn.OIDC.SessionTTL = scopes, ttl
	c.Authn.OIDC.Issuer = "ftp://idp.example"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("err = %v", err)
	}
}

// Protocol notices (docs/security.md, "Protocol rejections"): defaults are valid
// and change no behavior; each field is validated.
func TestValidate_ProtocolNotice(t *testing.T) {
	base := Defaults()
	if base.Security.MinHTTPVersion != "1.1" || base.Security.AltSvcMaxAge != 86400 || base.TLS.ClientCertNotice ||
		base.TLS.Notice != (NoticeConfig{}) {
		t.Fatalf("defaults: min_http_version %q alt_svc_max_age %d client_cert_notice %v notice %+v",
			base.Security.MinHTTPVersion, base.Security.AltSvcMaxAge, base.TLS.ClientCertNotice, base.TLS.Notice)
	}
	if err := Validate(base); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}

	for _, tc := range []struct {
		name string
		edit func(*Config)
		want string // "" → valid
	}{
		{"min empty means 1.1", func(c *Config) { c.Security.MinHTTPVersion = "" }, ""},
		{"min 2", func(c *Config) { c.Security.MinHTTPVersion = "2" }, ""},
		{"min 3 with h3", func(c *Config) {
			c.Security.MinHTTPVersion = "3"
			c.Listeners.H3.Enabled = true
			c.TLS.CertFile, c.TLS.KeyFile = "c.pem", "k.pem"
		}, ""},
		{"min 3 without h3", func(c *Config) { c.Security.MinHTTPVersion = "3" }, "min_http_version"},
		{"min 1.0", func(c *Config) { c.Security.MinHTTPVersion = "1.0" }, "min_http_version"},
		{"negative alt-svc", func(c *Config) { c.Security.AltSvcMaxAge = -1 }, "alt_svc_max_age"},
		{"base_url https", func(c *Config) { c.TLS.Notice.BaseURL = "https://notice.example" }, ""},
		{"base_url relative", func(c *Config) { c.TLS.Notice.BaseURL = "/notice" }, "base_url"},
		{"base_url with query", func(c *Config) { c.TLS.Notice.BaseURL = "http://n.example/?x=1" }, "base_url"},
		{"help_url ftp", func(c *Config) { c.TLS.Notice.HelpURL = "ftp://help.example" }, "help_url"},
		{"help_url https", func(c *Config) { c.TLS.Notice.HelpURL = "https://help.example/upgrade" }, ""},
	} {
		c := Defaults()
		tc.edit(&c)
		err := Validate(c)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: rejected: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.want)
		}
	}
}

func TestApplyEnv_ProtocolNotice(t *testing.T) {
	t.Setenv("KEEL_SECURITY_MIN_HTTP_VERSION", "2")
	t.Setenv("KEEL_SECURITY_ALT_SVC_MAX_AGE", "60")
	t.Setenv("KEEL_TLS_NOTICE_BASE_URL", "https://n.example")
	t.Setenv("KEEL_TLS_NOTICE_HELP_URL", "https://h.example")
	t.Setenv("KEEL_TLS_NOTICE_CONTACT", "ops@example.com")
	t.Setenv("KEEL_TLS_CLIENT_CERT_NOTICE", "true")
	c, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Security.MinHTTPVersion != "2" || c.Security.AltSvcMaxAge != 60 || !c.TLS.ClientCertNotice ||
		c.TLS.Notice != (NoticeConfig{BaseURL: "https://n.example", HelpURL: "https://h.example", Contact: "ops@example.com"}) {
		t.Fatalf("env not applied: %+v %+v", c.Security, c.TLS)
	}
}

// A config built without Defaults() still reaches every other check: the protocol
// settings' zero values are their defaults.
func TestValidate_ZeroConfigReachesTheOtherChecks(t *testing.T) {
	cfg := Config{}
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.com"}
	cfg.TLS.ACME.ChallengePort = 8080
	t.Setenv("BATS_TEST_FILENAME", "")
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "challenge_port") {
		t.Fatalf("err = %v, want the challenge_port check", err)
	}
}

// sidecar.retry: defaults are valid; a non-positive attempt count or a negative
// backoff is rejected only when the sidecar is enabled.
func TestValidate_SidecarRetry(t *testing.T) {
	base := Defaults()
	base.Sidecar.Enabled = true
	base.Sidecar.UpstreamURL = "http://app:8080"
	if base.Sidecar.Retry.DialAttempts != 3 || base.Sidecar.Retry.DialBackoff.Duration != 50*time.Millisecond {
		t.Fatalf("defaults = %+v", base.Sidecar.Retry)
	}
	if err := Validate(base); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}

	zero := base
	zero.Sidecar.Retry.DialAttempts = 0
	if err := Validate(zero); err == nil || !strings.Contains(err.Error(), "dial_attempts") {
		t.Errorf("dial_attempts 0: err = %v", err)
	}

	neg := base
	neg.Sidecar.Retry.DialBackoff = DurationOf(-time.Millisecond)
	if err := Validate(neg); err == nil || !strings.Contains(err.Error(), "dial_backoff") {
		t.Errorf("negative dial_backoff: err = %v", err)
	}

	off := zero
	off.Sidecar.Enabled = false
	off.Sidecar.Retry.DialBackoff = DurationOf(-time.Millisecond)
	if err := Validate(off); err != nil {
		t.Errorf("sidecar disabled: retry fields must not be validated: %v", err)
	}
}

// KEEL_SIDECAR_RETRY_DIAL_ATTEMPTS / KEEL_SIDECAR_RETRY_DIAL_BACKOFF override the YAML values.
func TestApplyEnv_SidecarRetry(t *testing.T) {
	t.Setenv("KEEL_SIDECAR_RETRY_DIAL_ATTEMPTS", "5")
	t.Setenv("KEEL_SIDECAR_RETRY_DIAL_BACKOFF", "200ms")
	cfg, err := load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sidecar.Retry.DialAttempts != 5 || cfg.Sidecar.Retry.DialBackoff.Duration != 200*time.Millisecond {
		t.Fatalf("env overrides not applied: %+v", cfg.Sidecar.Retry)
	}
}

// upstream_tls is incompatible with a unix:// upstream and with plaintext h2c.
func TestValidate_SidecarUpstreamTLSConflicts(t *testing.T) {
	base := Defaults()
	base.Sidecar.Enabled = true
	base.Sidecar.UpstreamURL = "unix:///run/app.sock"
	base.Sidecar.UpstreamH2C = true
	if err := Validate(base); err != nil {
		t.Fatalf("unix socket + h2c rejected: %v", err)
	}

	sockTLS := base
	sockTLS.Sidecar.UpstreamH2C = false
	sockTLS.Sidecar.UpstreamTLS.Enabled = true
	if err := Validate(sockTLS); err == nil || !strings.Contains(err.Error(), "unix://") {
		t.Errorf("unix + tls: err = %v", err)
	}

	h2cTLS := base
	h2cTLS.Sidecar.UpstreamURL = "https://app:8443"
	h2cTLS.Sidecar.UpstreamTLS.Enabled = true
	if err := Validate(h2cTLS); err == nil || !strings.Contains(err.Error(), "upstream_h2c") {
		t.Errorf("h2c + tls: err = %v", err)
	}
}
