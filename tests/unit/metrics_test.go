//go:build !no_prom

package unit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/metrics"
)

func TestMetrics_HandlerContentType(t *testing.T) {
	m := metrics.New()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("content-type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %q", ct)
	}
}

func TestMetrics_RequestsTotal(t *testing.T) {
	m := metrics.New()
	h := m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Verify keel_requests_total appears in /metrics output with correct labels.
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rr.Body)
	out := string(body)

	if !strings.Contains(out, "keel_requests_total") {
		t.Error("expected keel_requests_total in /metrics output")
	}
	if !strings.Contains(out, `method="GET"`) {
		t.Errorf("expected method label in output, got:\n%s", out)
	}
	if !strings.Contains(out, `status="200"`) {
		t.Errorf("expected status label in output, got:\n%s", out)
	}
}

func TestMetrics_Inflight(t *testing.T) {
	m := metrics.New()

	var inflightDuring float64
	h := m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		inflightDuring = m.Inflight()
		w.WriteHeader(http.StatusOK)
	}))

	if before := m.Inflight(); before != 0 {
		t.Errorf("expected inflight=0 before request, got %g", before)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if inflightDuring != 1 {
		t.Errorf("expected inflight=1 during request, got %g", inflightDuring)
	}
	if after := m.Inflight(); after != 0 {
		t.Errorf("expected inflight=0 after request, got %g", after)
	}
}

func TestMetrics_FIPSActive(t *testing.T) {
	m := metrics.New()
	v := m.FIPSActive()
	if v != 0 && v != 1 {
		t.Errorf("keel_fips_active must be 0 or 1, got %g", v)
	}
	// In the default build (!no_fips), fipsActive = 1.
	if v != 1 {
		t.Errorf("expected keel_fips_active=1 in FIPS build, got %g", v)
	}
}

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