// tests/unit/config_test.go
package unit

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/ports"
)

// ---------------------------------------------------------------------------
// Duration.UnmarshalYAML
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// From
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// applyEnv helpers (tested via From / Load with env vars)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// AddrFromPort
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Schema embed
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// KnownFields enforcement
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// JSON Schema validation via applyYAMLFile (santhosh-tekuri/jsonschema/v5)
// ---------------------------------------------------------------------------

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
