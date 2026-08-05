// pkg/apps/myapp/myapp_test.go
package myapp

import (
	"testing"
)

// TestRun_ServerStartup drives Run end-to-end: it starts the embedded keel
// server and shuts it down via KEEL_TEST_SHUTDOWN_AFTER. HTTPS/H3 are disabled
// (so no TLS cert is required) and the remaining listeners use unique high ports
// to avoid colliding with other packages' server-startup tests running in
// parallel.
func TestRun_ServerStartup(t *testing.T) {
	t.Setenv("APP_CONFIG", "")
	t.Setenv("APP_SECRETS", "")
	t.Setenv("KEEL_HTTPS_ENABLED", "false")
	t.Setenv("KEEL_H3_ENABLED", "false")
	t.Setenv("KEEL_HTTP_PORT", "18080")
	t.Setenv("KEEL_HEALTH_PORT", "19091")
	t.Setenv("KEEL_READY_PORT", "19092")
	t.Setenv("KEEL_STARTUP_PORT", "19093")
	t.Setenv("KEEL_ADMIN_PORT", "19999")
	t.Setenv("KEEL_TEST_SHUTDOWN_AFTER", "100ms")

	if got := Run(); got != 0 {
		t.Errorf("expected return code 0, got %d", got)
	}
}
