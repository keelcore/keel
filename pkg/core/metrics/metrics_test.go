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

func TestUpstreamDialCounters(t *testing.T) {
	m := New()
	m.IncUpstreamDialRetry()
	m.IncUpstreamDialRetry()
	m.IncUpstreamDialFailure()

	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	for _, want := range []string{
		"# TYPE keel_upstream_dial_retries_total counter",
		"keel_upstream_dial_retries_total 2",
		"# TYPE keel_upstream_dial_failures_total counter",
		"keel_upstream_dial_failures_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
}

func TestUpstreamRetryAfterCounter(t *testing.T) {
	m := New()
	m.IncUpstreamRetryAfter()

	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	for _, want := range []string{
		"# TYPE keel_upstream_retry_after_total counter",
		"keel_upstream_retry_after_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
}

// Arbitrary client-chosen methods must not mint new series: they collapse to a
// single method="OTHER" label (unbounded-cardinality memory DoS regression).
func TestInstrument_NonStandardMethodsCollapseToOther(t *testing.T) {
	m := New()
	h := m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	for _, method := range []string{"AAAA1", "AAAA2", "GET|#x"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/", nil))
	}
	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	if !strings.Contains(out, `method="OTHER",status="200"`) {
		t.Fatalf("expected OTHER label, got %q", out)
	}
	for _, leaked := range []string{"AAAA1", "AAAA2", "GET|#x"} {
		if strings.Contains(out, leaked) {
			t.Errorf("raw method %q leaked into metrics: %q", leaked, out)
		}
	}
}

// U2: rejections, 301s sent and notice pages served are counted by code.
func TestProtocolRejectionCounters(t *testing.T) {
	m := New()
	m.IncTLSHandshakeRejection("KEEL-PROTO-001")
	m.IncTLSHandshakeRejection("KEEL-PROTO-001")
	m.IncTLSHandshakeRejection("KEEL-PROTO-003")
	m.IncProtocolRejection("KEEL-PROTO-006")
	m.IncProtocolNotice("KEEL-PROTO-006", "json")

	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	for _, want := range []string{
		"# TYPE keel_tls_handshake_rejections_total counter",
		`keel_tls_handshake_rejections_total{code="KEEL-PROTO-001"} 2`,
		`keel_tls_handshake_rejections_total{code="KEEL-PROTO-003"} 1`,
		"# TYPE keel_protocol_rejections_total counter",
		`keel_protocol_rejections_total{code="KEEL-PROTO-006"} 1`,
		"# TYPE keel_protocol_notices_served_total counter",
		`keel_protocol_notices_served_total{code="KEEL-PROTO-006",format="json"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
}

// IncFIPSMonitorFailure increments a counter visible in /metrics output.
func TestMetrics_IncFIPSMonitorFailure_AppearsInOutput(t *testing.T) {
	m := New()
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
	m := New()
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
	m := New()

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

func TestMetrics_HandlerContentType(t *testing.T) {
	m := New()
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
	m := New()
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
	m := New()

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

// SetCertExpiry: stores the value; verifiable via the /metrics handler output.
func TestMetrics_SetCertExpiry(t *testing.T) {
	m := New()
	m.SetCertExpiry(3600.0)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))

	if !strings.Contains(rr.Body.String(), "keel_tls_cert_expiry_seconds 3600") {
		t.Errorf("expected cert expiry 3600 in metrics, got: %s", rr.Body.String())
	}
}

// SetLogDrops: stores the value; verifiable via the /metrics handler output.
func TestMetrics_SetLogDrops(t *testing.T) {
	m := New()
	m.SetLogDrops(42)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))

	if !strings.Contains(rr.Body.String(), "keel_log_drops_total 42") {
		t.Errorf("expected log drops 42 in metrics, got: %s", rr.Body.String())
	}
}
