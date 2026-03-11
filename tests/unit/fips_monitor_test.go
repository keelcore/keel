//go:build !no_prom

// tests/unit/fips_monitor_test.go
package unit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/metrics"
)

// IncFIPSMonitorFailure increments a counter visible in /metrics output.
func TestMetrics_IncFIPSMonitorFailure_AppearsInOutput(t *testing.T) {
	m := metrics.New()
	m.IncFIPSMonitorFailure()
	m.IncFIPSMonitorFailure()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	if !strings.Contains(body, "keel_fips_monitor_failures_total") {
		t.Errorf("expected keel_fips_monitor_failures_total in /metrics output, got:\n%s", body)
	}
}

// IncFIPSMonitorFailure is cumulative: two calls produce count 2.
func TestMetrics_IncFIPSMonitorFailure_Cumulative(t *testing.T) {
	m := metrics.New()
	m.IncFIPSMonitorFailure()
	m.IncFIPSMonitorFailure()
	m.IncFIPSMonitorFailure()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	if !strings.Contains(body, "keel_fips_monitor_failures_total 3") {
		t.Errorf("expected count 3, got:\n%s", body)
	}
}

// A fresh Metrics instance starts at zero (counter absent or zero).
func TestMetrics_IncFIPSMonitorFailure_StartsAtZero(t *testing.T) {
	m := metrics.New()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	// Counter at zero may be omitted or present as 0 — either is acceptable.
	// It must NOT show a non-zero value.
	if strings.Contains(body, "keel_fips_monitor_failures_total") &&
		!strings.Contains(body, "keel_fips_monitor_failures_total 0") {
		t.Errorf("expected zero count, got:\n%s", body)
	}
}
