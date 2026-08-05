// pkg/apps/myapp/args_test.go
package myapp

import (
	"testing"
)

func TestProcessArgs_LoadsConfig(t *testing.T) {
	// No terminal flags are set (the test harness parses only -test.* flags),
	// so TryVersion and TryValidateApp fall through and processArgs returns the
	// loaded config.
	cfgPath := writeTempFile(t, "config.yaml", "app:\n  name: args-test\n")
	t.Setenv("APP_CONFIG", cfgPath)
	t.Setenv("APP_SECRETS", "")

	log, exits := newTestLogger(t)
	cfg := processArgs(log)

	if *exits != 0 {
		t.Fatalf("expected no exit, got %d", *exits)
	}
	if cfg.App.Name != "args-test" {
		t.Errorf("expected app name args-test, got %q", cfg.App.Name)
	}
}
