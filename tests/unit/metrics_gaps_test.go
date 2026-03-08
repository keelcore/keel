//go:build !no_prom

// tests/unit/metrics_gaps_test.go
package unit

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/metrics"
)

// SetCertExpiry: stores the value; verifiable via the /metrics handler output.
func TestMetrics_SetCertExpiry(t *testing.T) {
	m := metrics.New()
	m.SetCertExpiry(3600.0)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))

	if !strings.Contains(rr.Body.String(), "keel_tls_cert_expiry_seconds 3600") {
		t.Errorf("expected cert expiry 3600 in metrics, got: %s", rr.Body.String())
	}
}

// SetLogDrops: stores the value; verifiable via the /metrics handler output.
func TestMetrics_SetLogDrops(t *testing.T) {
	m := metrics.New()
	m.SetLogDrops(42)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))

	if !strings.Contains(rr.Body.String(), "keel_log_drops_total 42") {
		t.Errorf("expected log drops 42 in metrics, got: %s", rr.Body.String())
	}
}