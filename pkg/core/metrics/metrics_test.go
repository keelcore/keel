//go:build !no_prom

package metrics

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLabelCounter(t *testing.T) {
	c := newLabelCounter()
	c.inc(`method="GET"`)
	c.inc(`method="GET"`)
	c.inc(`method="POST"`)
	var buf bytes.Buffer
	c.writeTo(&buf, "keel_requests_total", "Total requests.")
	out := buf.String()
	if !strings.Contains(out, "# HELP keel_requests_total Total requests.") {
		t.Fatalf("missing HELP line: %q", out)
	}
	if !strings.Contains(out, `keel_requests_total{method="GET"} 2`) {
		t.Fatalf("missing GET count: %q", out)
	}
	if !strings.Contains(out, `keel_requests_total{method="POST"} 1`) {
		t.Fatalf("missing POST count: %q", out)
	}
}

func TestInflightGauge(t *testing.T) {
	var g inflightGauge
	if g.get() != 0 {
		t.Fatalf("want 0, got %g", g.get())
	}
	g.inc()
	g.inc()
	if g.get() != 2 {
		t.Fatalf("want 2, got %g", g.get())
	}
	g.dec()
	if g.get() != 1 {
		t.Fatalf("want 1, got %g", g.get())
	}
}

// stubCollector records a fixed series to prove Register + collector loop.
type stubCollector struct{ line string }

func (s stubCollector) Collect(w io.Writer) { _, _ = io.WriteString(w, s.line) }

func TestNewAndRegister(t *testing.T) {
	m := New()
	if m.requests == nil {
		t.Fatal("requests not initialized")
	}
	m.Register(stubCollector{line: "custom_series 42\n"})
	var buf bytes.Buffer
	m.writeTo(&buf)
	if !strings.Contains(buf.String(), "custom_series 42") {
		t.Fatalf("collector series missing: %q", buf.String())
	}
}

func TestSettersAndAccessors(t *testing.T) {
	m := New()
	m.inflight.inc()
	if m.Inflight() != 1 {
		t.Fatalf("Inflight want 1, got %g", m.Inflight())
	}
	if fa := m.FIPSActive(); fa != fipsActive {
		t.Fatalf("FIPSActive want %g, got %g", fipsActive, fa)
	}
	m.SetCertExpiry(123)
	m.SetLogDrops(7)
	m.IncFIPSMonitorFailure()
	m.IncFIPSMonitorFailure()

	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	for _, want := range []string{
		"keel_http_inflight_requests 1",
		"keel_fips_active",
		"keel_tls_cert_expiry_seconds 123",
		"keel_log_drops_total 7",
		"keel_fips_monitor_failures_total 2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
}

func TestWriteToNoFIPSFailures(t *testing.T) {
	// With zero FIPS monitor failures, the counter block must be omitted.
	m := New()
	var buf bytes.Buffer
	m.writeTo(&buf)
	if strings.Contains(buf.String(), "keel_fips_monitor_failures_total") {
		t.Fatalf("failures counter should be absent: %q", buf.String())
	}
}

func TestHandler(t *testing.T) {
	m := New()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if ct := rec.Header().Get("content-type"); ct != "text/plain; version=0.0.4" {
		t.Fatalf("unexpected content-type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "keel_http_inflight_requests") {
		t.Fatalf("body missing metrics: %q", rec.Body.String())
	}
}

func TestInstrument(t *testing.T) {
	m := New()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // exercises statusCapture.WriteHeader
		_, _ = io.WriteString(w, "ok")
	})
	h := m.Instrument(next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status not propagated, got %d", rec.Code)
	}
	var buf bytes.Buffer
	m.writeTo(&buf)
	if !strings.Contains(buf.String(), `method="POST",status="418"`) {
		t.Fatalf("request not recorded: %q", buf.String())
	}
}

func TestStatusCaptureUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sc := &statusCapture{ResponseWriter: rec, status: http.StatusOK}
	if sc.Unwrap() != rec {
		t.Fatal("Unwrap did not return wrapped writer")
	}
}
