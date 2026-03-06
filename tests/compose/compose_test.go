// tests/compose/compose_test.go
// Integration tests that run against the live docker-compose.test.yaml topology.
//
// These tests are skipped when KEEL_COMPOSE_TESTS is unset.
// Run via: scripts/test/compose.sh  OR
//          KEEL_COMPOSE_TESTS=1 go test ./tests/compose/...
package compose

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// skip returns true (and calls t.Skip) if the compose harness is not running.
func skip(t *testing.T) {
	t.Helper()
	if os.Getenv("KEEL_COMPOSE_TESTS") == "" {
		t.Skip("set KEEL_COMPOSE_TESTS=1 to run compose integration tests")
	}
}

func keelURL(port int, path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
}

// waitHTTP polls url until it returns wantStatus or the deadline passes.
func waitHTTP(t *testing.T, url string, wantStatus int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:gosec
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == wantStatus {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout: %s did not return %d within %s", url, wantStatus, timeout)
}

// ── Probe endpoint tests ──────────────────────────────────────────────────────

func TestCompose_HealthProbe(t *testing.T) {
	skip(t)
	waitHTTP(t, keelURL(KeelHealth, "/healthz"), http.StatusOK, 30*time.Second)
}

func TestCompose_ReadyProbe(t *testing.T) {
	skip(t)
	// Ready probe may return 200 or 503 depending on state; just verify it responds.
	url := keelURL(KeelReady, "/readyz")
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:gosec
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusServiceUnavailable {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout: %s did not respond within 30s", url)
}

func TestCompose_StartupProbe(t *testing.T) {
	skip(t)
	// /startupz returns 503 until startup complete, then 200.
	url := keelURL(KeelStartup, "/startupz")
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:gosec
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout: %s did not return 200 within 30s", url)
}

// ── Main HTTP listener tests ──────────────────────────────────────────────────

func TestCompose_MainHTTPRoot(t *testing.T) {
	skip(t)
	waitHTTP(t, keelURL(KeelHealth, "/healthz"), http.StatusOK, 30*time.Second) // ensure up
	waitHTTP(t, keelURL(KeelHTTP, "/"), http.StatusOK, 5*time.Second)
}

func TestCompose_ProbesNotOnMainPort(t *testing.T) {
	skip(t)
	waitHTTP(t, keelURL(KeelHealth, "/healthz"), http.StatusOK, 30*time.Second)

	// Probe routes must NOT be accessible on the main HTTP port.
	waitHTTP(t, keelURL(KeelHTTP, "/healthz"), http.StatusNotFound, 5*time.Second)
	waitHTTP(t, keelURL(KeelHTTP, "/readyz"), http.StatusNotFound, 5*time.Second)
}

// ── OWASP header tests ────────────────────────────────────────────────────────

func TestCompose_OWASPHeaders(t *testing.T) {
	skip(t)
	waitHTTP(t, keelURL(KeelHealth, "/healthz"), http.StatusOK, 30*time.Second)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(keelURL(KeelHTTP, "/")) //nolint:gosec
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_ = resp.Body.Close()

	required := []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Content-Security-Policy",
		"Referrer-Policy",
	}
	for _, h := range required {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing OWASP header: %s", h)
		}
	}
}

// ── Admin port tests ──────────────────────────────────────────────────────────

func TestCompose_AdminPort(t *testing.T) {
	skip(t)
	waitHTTP(t, keelURL(KeelHealth, "/healthz"), http.StatusOK, 30*time.Second)
	// Admin port should be listening (exact routes depend on P15 implementation).
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(keelURL(KeelAdmin, "/")) //nolint:gosec
	if err != nil {
		t.Fatalf("admin port not reachable: %v", err)
	}
	_ = resp.Body.Close()
	// Accept any response (200, 404) — just verify the port is open.
}

// ── Upstream reachability (topology smoke test) ────────────────────────────

func TestCompose_UpstreamDirectlyReachable(t *testing.T) {
	skip(t)
	// Verify the upstream echo server is reachable from the test host.
	// This is a topology sanity check, not a keel test.
	waitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", Upstream), http.StatusOK, 15*time.Second)
}

// ── Prometheus reachability ───────────────────────────────────────────────────

func TestCompose_PrometheusReachable(t *testing.T) {
	skip(t)
	waitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/-/healthy", Prometheus), http.StatusOK, 30*time.Second)
}