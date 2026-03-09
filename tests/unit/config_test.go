// tests/unit/config_test.go
package unit

import (
	"io"
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/ports"
)

// ---------------------------------------------------------------------------
// Duration.UnmarshalYAML
// ---------------------------------------------------------------------------

func TestDuration_UnmarshalYAML_Valid(t *testing.T) {
	var d config.Duration
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "5s"}
	if err := d.UnmarshalYAML(node); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Duration != 5*time.Second {
		t.Errorf("got %v, want 5s", d.Duration)
	}
}

func TestDuration_UnmarshalYAML_InvalidString(t *testing.T) {
	var d config.Duration
	// Sequence node cannot decode to string.
	node := &yaml.Node{Kind: yaml.SequenceNode}
	if err := d.UnmarshalYAML(node); err == nil {
		t.Fatal("expected error for non-string node, got nil")
	}
}

func TestDuration_UnmarshalYAML_InvalidDuration(t *testing.T) {
	var d config.Duration
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "notaduration"}
	if err := d.UnmarshalYAML(node); err == nil {
		t.Fatal("expected error for invalid duration string, got nil")
	}
}

func TestDurationOf(t *testing.T) {
	d := config.DurationOf(10 * time.Millisecond)
	if d.Duration != 10*time.Millisecond {
		t.Errorf("got %v, want 10ms", d.Duration)
	}
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

func TestDefaults_KeyValues(t *testing.T) {
	cfg := config.Defaults()

	if !cfg.Listeners.HTTP.Enabled {
		t.Error("HTTP listener should be enabled by default")
	}
	if cfg.Listeners.HTTP.Port != ports.HTTP {
		t.Errorf("HTTP port: got %d, want %d", cfg.Listeners.HTTP.Port, ports.HTTP)
	}
	if cfg.Listeners.HTTPS.Enabled {
		t.Error("HTTPS listener should be disabled by default")
	}
	if !cfg.Authn.Enabled {
		t.Error("authn should be enabled by default")
	}
	if !cfg.Metrics.Prometheus {
		t.Error("prometheus should be enabled by default")
	}
	if cfg.Security.MaxHeaderBytes != 65536 {
		t.Errorf("MaxHeaderBytes: got %d, want 65536", cfg.Security.MaxHeaderBytes)
	}
	if cfg.Backpressure.HighWatermark != 0.85 {
		t.Errorf("HighWatermark: got %f, want 0.85", cfg.Backpressure.HighWatermark)
	}
	if cfg.Timeouts.Read.Duration != 30*time.Second {
		t.Errorf("Read timeout: got %v, want 30s", cfg.Timeouts.Read.Duration)
	}
}

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

func TestValidate_HappyPath(t *testing.T) {
	cfg := config.Defaults()
	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid default config rejected: %v", err)
	}
}

func TestValidate_HTTPSEnabledNoCert(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTPS.Enabled = true
	if err := config.Validate(cfg); err == nil {
		t.Fatal("expected error: HTTPS enabled without cert")
	}
}

func TestValidate_H3EnabledNoCert(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.H3.Enabled = true
	if err := config.Validate(cfg); err == nil {
		t.Fatal("expected error: H3 enabled without cert")
	}
}

func TestValidate_ACMEAndCertConflict(t *testing.T) {
	cfg := config.Defaults()
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.com"}
	cfg.TLS.CertFile = "cert.pem"
	cfg.TLS.KeyFile = "key.pem"
	if err := config.Validate(cfg); err == nil {
		t.Fatal("expected error: ACME + static cert conflict")
	}
}

func TestValidate_ACMENoDomains(t *testing.T) {
	cfg := config.Defaults()
	cfg.TLS.ACME.Enabled = true
	if err := config.Validate(cfg); err == nil {
		t.Fatal("expected error: ACME enabled without domains")
	}
}

func TestValidate_BackpressureWatermarkInverted(t *testing.T) {
	cfg := config.Defaults()
	cfg.Backpressure.HighWatermark = 0.5
	cfg.Backpressure.LowWatermark = 0.8
	if err := config.Validate(cfg); err == nil {
		t.Fatal("expected error: low_watermark >= high_watermark")
	}
}

func TestValidate_SidecarEnabledNoURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = ""
	if err := config.Validate(cfg); err == nil {
		t.Fatal("expected error: sidecar enabled without upstream_url")
	}
}

func TestValidate_SidecarWithURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = "http://localhost:9000"
	if err := config.Validate(cfg); err != nil {
		t.Errorf("unexpected error with valid sidecar config: %v", err)
	}
}

func TestValidate_ACMEValidPath(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.com"}
	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid ACME config rejected: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

func TestLoad_EmptyPaths_UsesDefaults(t *testing.T) {
	cfg, err := config.Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listeners.HTTP.Port != ports.HTTP {
		t.Errorf("HTTP port: got %d, want %d", cfg.Listeners.HTTP.Port, ports.HTTP)
	}
}

func TestLoad_ValidConfigFile(t *testing.T) {
	f, err := os.CreateTemp("", "keel-cfg-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	_, _ = f.WriteString("listeners:\n  http:\n    enabled: false\n")
	f.Close()

	cfg, err := config.Load(f.Name(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listeners.HTTP.Enabled {
		t.Error("HTTP should be disabled per config file")
	}
}

func TestLoad_ValidSecretsFile(t *testing.T) {
	f, err := os.CreateTemp("", "keel-sec-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	_, _ = f.WriteString("authn:\n  my_id: myservice\n")
	f.Close()

	cfg, err := config.Load("", f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Authn.MyID != "myservice" {
		t.Errorf("MyID: got %q, want %q", cfg.Authn.MyID, "myservice")
	}
}

func TestLoad_BadConfigPath(t *testing.T) {
	_, err := config.Load("/nonexistent/path/keel.yaml", "")
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "keel-bad-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	_, _ = f.WriteString(":\t bad yaml {{{\n")
	f.Close()

	_, err = config.Load(f.Name(), "")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_ValidationError(t *testing.T) {
	f, err := os.CreateTemp("", "keel-val-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	_, _ = f.WriteString("listeners:\n  https:\n    enabled: true\n")
	f.Close()

	_, err = config.Load(f.Name(), "")
	if err == nil {
		t.Fatal("expected validation error: HTTPS enabled without cert")
	}
}

// ---------------------------------------------------------------------------
// From
// ---------------------------------------------------------------------------

func TestFrom_EnvOverridesPort(t *testing.T) {
	t.Setenv("KEEL_HTTP_PORT", "19080")
	defer os.Unsetenv("KEEL_HTTP_PORT")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listeners.HTTP.Port != 19080 {
		t.Errorf("HTTP port: got %d, want 19080", cfg.Listeners.HTTP.Port)
	}
}

func TestFrom_ValidationError(t *testing.T) {
	base := config.Defaults()
	base.Listeners.HTTPS.Enabled = true
	_, err := config.From(&base)
	if err == nil {
		t.Fatal("expected validation error from From()")
	}
}

func TestFrom_DoesNotMutateInput(t *testing.T) {
	base := config.Defaults()
	original := base.Listeners.HTTP.Port
	t.Setenv("KEEL_HTTP_PORT", "19081")
	defer os.Unsetenv("KEEL_HTTP_PORT")

	_, _ = config.From(&base)
	if base.Listeners.HTTP.Port != original {
		t.Error("From() must not mutate the input config")
	}
}

// ---------------------------------------------------------------------------
// applyEnv helpers (tested via From / Load with env vars)
// ---------------------------------------------------------------------------

func TestApplyEnv_Bool_True(t *testing.T) {
	t.Setenv("KEEL_HTTPS_ENABLED", "true")
	defer os.Unsetenv("KEEL_HTTPS_ENABLED")
	t.Setenv("KEEL_TLS_CERT", "c.pem")
	defer os.Unsetenv("KEEL_TLS_CERT")
	t.Setenv("KEEL_TLS_KEY", "k.pem")
	defer os.Unsetenv("KEEL_TLS_KEY")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Listeners.HTTPS.Enabled {
		t.Error("HTTPS should be enabled via env")
	}
}

func TestApplyEnv_Bool_Invalid_IsIgnored(t *testing.T) {
	t.Setenv("KEEL_HTTP_ENABLED", "notabool")
	defer os.Unsetenv("KEEL_HTTP_ENABLED")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// invalid bool is silently ignored; default (true) is retained
	if !cfg.Listeners.HTTP.Enabled {
		t.Error("HTTP should retain default true when env value is invalid")
	}
}

func TestApplyEnv_Int_Valid(t *testing.T) {
	t.Setenv("KEEL_HEALTH_PORT", "19091")
	defer os.Unsetenv("KEEL_HEALTH_PORT")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listeners.Health.Port != 19091 {
		t.Errorf("health port: got %d, want 19091", cfg.Listeners.Health.Port)
	}
}

func TestApplyEnv_Int_Invalid_IsIgnored(t *testing.T) {
	t.Setenv("KEEL_HEALTH_PORT", "notanint")
	defer os.Unsetenv("KEEL_HEALTH_PORT")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listeners.Health.Port != ports.HEALTH {
		t.Errorf("health port should retain default %d, got %d", ports.HEALTH, cfg.Listeners.Health.Port)
	}
}

func TestApplyEnv_Int64_Valid(t *testing.T) {
	t.Setenv("KEEL_MAX_REQ_BODY_BYTES", "1048576")
	defer os.Unsetenv("KEEL_MAX_REQ_BODY_BYTES")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Security.MaxRequestBodyBytes != 1048576 {
		t.Errorf("MaxRequestBodyBytes: got %d, want 1048576", cfg.Security.MaxRequestBodyBytes)
	}
}

func TestApplyEnv_Int64_Invalid_IsIgnored(t *testing.T) {
	t.Setenv("KEEL_MAX_REQ_BODY_BYTES", "bad")
	defer os.Unsetenv("KEEL_MAX_REQ_BODY_BYTES")

	base := config.Defaults()
	orig := base.Security.MaxRequestBodyBytes
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Security.MaxRequestBodyBytes != orig {
		t.Error("invalid int64 env should be ignored")
	}
}

func TestApplyEnv_Float64_Valid(t *testing.T) {
	t.Setenv("KEEL_PRESSURE_HIGH", "0.90")
	defer os.Unsetenv("KEEL_PRESSURE_HIGH")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backpressure.HighWatermark != 0.90 {
		t.Errorf("HighWatermark: got %f, want 0.90", cfg.Backpressure.HighWatermark)
	}
}

func TestApplyEnv_Float64_Invalid_IsIgnored(t *testing.T) {
	t.Setenv("KEEL_PRESSURE_HIGH", "bad")
	defer os.Unsetenv("KEEL_PRESSURE_HIGH")

	base := config.Defaults()
	orig := base.Backpressure.HighWatermark
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backpressure.HighWatermark != orig {
		t.Error("invalid float64 env should be ignored")
	}
}

func TestApplyEnv_String_Set(t *testing.T) {
	t.Setenv("KEEL_LOG_LEVEL", "debug")
	defer os.Unsetenv("KEEL_LOG_LEVEL")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Level: got %q, want %q", cfg.Logging.Level, "debug")
	}
}

func TestApplyEnv_Duration_Valid(t *testing.T) {
	t.Setenv("KEEL_READ_TIMEOUT", "45s")
	defer os.Unsetenv("KEEL_READ_TIMEOUT")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeouts.Read.Duration != 45*time.Second {
		t.Errorf("Read timeout: got %v, want 45s", cfg.Timeouts.Read.Duration)
	}
}

func TestApplyEnv_Duration_Invalid_IsIgnored(t *testing.T) {
	t.Setenv("KEEL_READ_TIMEOUT", "notaduration")
	defer os.Unsetenv("KEEL_READ_TIMEOUT")

	base := config.Defaults()
	orig := base.Timeouts.Read.Duration
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeouts.Read.Duration != orig {
		t.Error("invalid duration env should be ignored")
	}
}

func TestApplyEnv_CSV_Multi(t *testing.T) {
	t.Setenv("KEEL_TRUSTED_IDS", "svc-a, svc-b , svc-c")
	defer os.Unsetenv("KEEL_TRUSTED_IDS")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Authn.TrustedIDs) != 3 {
		t.Errorf("TrustedIDs: got %v, want 3 entries", cfg.Authn.TrustedIDs)
	}
	if cfg.Authn.TrustedIDs[1] != "svc-b" {
		t.Errorf("TrustedIDs[1]: got %q, want svc-b", cfg.Authn.TrustedIDs[1])
	}
}

func TestApplyEnv_CSV_AllEmpty_IsIgnored(t *testing.T) {
	t.Setenv("KEEL_TRUSTED_IDS", " , , ")
	defer os.Unsetenv("KEEL_TRUSTED_IDS")

	base := config.Defaults()
	cfg, err := config.From(&base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Authn.TrustedIDs) != 0 {
		t.Errorf("all-empty CSV should not override; got %v", cfg.Authn.TrustedIDs)
	}
}

func TestLoad_BadSecretsPath(t *testing.T) {
	_, err := config.Load("", "/nonexistent/secrets.yaml")
	if err == nil {
		t.Fatal("expected error for missing secrets file")
	}
}

// ---------------------------------------------------------------------------
// AddrFromPort
// ---------------------------------------------------------------------------

func TestAddrFromPort(t *testing.T) {
	if got := config.AddrFromPort(8080); got != ":8080" {
		t.Errorf("got %q, want %q", got, ":8080")
	}
}

// Default: happy path — KEEL_CONFIG and KEEL_SECRETS unset, returns defaults-based config.
func TestConfigDefault_ReturnsValidConfig(t *testing.T) {
	t.Setenv("KEEL_CONFIG", "")
	t.Setenv("KEEL_SECRETS", "")
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Default(log)
	if cfg.Listeners.HTTP.Port != ports.HTTP {
		t.Errorf("expected HTTP port %d from Default, got %d", ports.HTTP, cfg.Listeners.HTTP.Port)
	}
}
