package config

import (
	"strings"
	"testing"
)

// Unparseable env overrides are reported (not silently dropped) and the prior
// value stays in force.
func TestLoad_ReportsIgnoredEnv(t *testing.T) {
	t.Setenv("KEEL_AUTHN", "flase")
	t.Setenv("KEEL_HTTP_PORT", "eighty")
	t.Setenv("KEEL_HEAP_MAX_BYTES", "lots")
	t.Setenv("KEEL_PRESSURE_HIGH", "high")
	t.Setenv("KEEL_SHUTDOWN_DRAIN", "soon")

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := strings.Join(cfg.IgnoredEnv(), "\n")
	for _, want := range []string{
		"KEEL_AUTHN=flase", "KEEL_HTTP_PORT=eighty", "KEEL_HEAP_MAX_BYTES=lots",
		"KEEL_PRESSURE_HIGH=high", "KEEL_SHUTDOWN_DRAIN=soon",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("IgnoredEnv missing %q; got:\n%s", want, got)
		}
	}
	d := Defaults()
	if cfg.Authn.Enabled != d.Authn.Enabled || cfg.Listeners.HTTP.Port != d.Listeners.HTTP.Port {
		t.Error("an ignored env override changed the config")
	}
}

// Valid overrides are not reported.
func TestLoad_NoIgnoredEnvWhenValid(t *testing.T) {
	t.Setenv("KEEL_AUTHN", "true")
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.IgnoredEnv()) != 0 {
		t.Fatalf("IgnoredEnv = %v, want none", cfg.IgnoredEnv())
	}
}
