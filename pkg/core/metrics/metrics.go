//go:build !no_prom

package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/keelcore/keel/pkg/core/httpx"
)

// labelCounter is a mutex-protected counter keyed by a Prometheus label string.
type labelCounter struct {
	mu     sync.Mutex
	counts map[string]uint64
}

func newLabelCounter() *labelCounter {
	return &labelCounter{counts: make(map[string]uint64)}
}

func (c *labelCounter) inc(labels string) {
	c.mu.Lock()
	c.counts[labels]++
	c.mu.Unlock()
}

func (c *labelCounter) writeTo(w io.Writer, name, help string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	for labels, v := range c.counts {
		fmt.Fprintf(w, "%s{%s} %d\n", name, labels, v)
	}
}

// inflightGauge tracks in-flight requests using an atomic int64.
type inflightGauge struct{ v int64 }

func (g *inflightGauge) inc() { atomic.AddInt64(&g.v, 1) }
func (g *inflightGauge) dec() { atomic.AddInt64(&g.v, -1) }
func (g *inflightGauge) get() float64 {
	return float64(atomic.LoadInt64(&g.v))
}

// Metrics holds all instrumentation state.
type Metrics struct {
	requests            *labelCounter
	tlsRejections       *labelCounter // by code: rejected TLS handshakes
	protoRejections     *labelCounter // by code: in-protocol rejections sent (301)
	protoNotices        *labelCounter // by code and format: notice pages served
	inflight            inflightGauge
	certExpirySecs      int64 // atomic; seconds until TLS cert expiry
	logDrops            int64 // atomic; cumulative remote-log drop count
	fipsMonitorFailures int64 // atomic; cumulative FIPS monitor check failures
	dialRetries         int64 // atomic; cumulative sidecar upstream dial retries
	dialFailures        int64 // atomic; sidecar requests whose every upstream dial failed
	retryAfters         int64 // atomic; sidecar requests retried after an upstream Retry-After
	mu                  sync.Mutex
	collectors          []Collector // embedder series, appended to the exposition
}

// Register adds an embedder Collector whose series are appended to the /metrics
// exposition after keel's own. Register before Run; safe for concurrent use.
func (m *Metrics) Register(c Collector) {
	m.mu.Lock()
	m.collectors = append(m.collectors, c)
	m.mu.Unlock()
}

// New creates a zeroed Metrics instance.
func New() *Metrics {
	return &Metrics{
		requests:        newLabelCounter(),
		tlsRejections:   newLabelCounter(),
		protoRejections: newLabelCounter(),
		protoNotices:    newLabelCounter(),
	}
}

// Handler returns an http.Handler that serves Prometheus text format on /metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain; version=0.0.4")
		m.writeTo(w)
	})
}

// Instrument wraps next to record RED metrics and the inflight gauge.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inflight.inc()
		defer m.inflight.dec()
		sc := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sc, r)
		labels := fmt.Sprintf(`method="%s",status="%d"`, httpx.MethodLabel(r.Method), sc.status)
		m.requests.inc(labels)
	})
}

// Inflight returns the current number of in-flight requests.
func (m *Metrics) Inflight() float64 { return m.inflight.get() }

// FIPSActive returns 1 if this is a FIPS build, 0 otherwise.
func (m *Metrics) FIPSActive() float64 { return fipsActive }

// SetCertExpiry records seconds until TLS certificate expiry (negative = expired).
func (m *Metrics) SetCertExpiry(secs float64) {
	atomic.StoreInt64(&m.certExpirySecs, int64(secs))
}

// SetLogDrops records cumulative remote-log lines dropped due to buffer overflow.
func (m *Metrics) SetLogDrops(drops int64) {
	atomic.StoreInt64(&m.logDrops, drops)
}

// IncFIPSMonitorFailure increments the counter of FIPS monitor check failures.
// Called by runFIPSMonitorLoop whenever fips.Check returns a non-nil error.
func (m *Metrics) IncFIPSMonitorFailure() {
	atomic.AddInt64(&m.fipsMonitorFailures, 1)
}

// IncUpstreamDialRetry counts a sidecar upstream connect that failed and is retried.
func (m *Metrics) IncUpstreamDialRetry() {
	atomic.AddInt64(&m.dialRetries, 1)
}

// IncUpstreamDialFailure counts a sidecar request whose every upstream connect failed.
func (m *Metrics) IncUpstreamDialFailure() {
	atomic.AddInt64(&m.dialFailures, 1)
}

// IncTLSHandshakeRejection counts a TLS handshake refused for code (KEEL-PROTO-*).
func (m *Metrics) IncTLSHandshakeRejection(code string) {
	m.tlsRejections.inc(fmt.Sprintf(`code="%s"`, code))
}

// IncProtocolRejection counts an in-protocol rejection (301 to the notice) for code.
func (m *Metrics) IncProtocolRejection(code string) {
	m.protoRejections.inc(fmt.Sprintf(`code="%s"`, code))
}

// IncProtocolNotice counts a notice page served for code in format (html, json, text).
func (m *Metrics) IncProtocolNotice(code, format string) {
	m.protoNotices.inc(fmt.Sprintf(`code="%s",format="%s"`, code, format))
}

// IncUpstreamRetryAfter counts a sidecar request retried after an upstream Retry-After.
func (m *Metrics) IncUpstreamRetryAfter() {
	atomic.AddInt64(&m.retryAfters, 1)
}

func (m *Metrics) writeTo(w io.Writer) {
	m.requests.writeTo(w, "keel_requests_total", "Total HTTP requests processed.")
	m.tlsRejections.writeTo(w, "keel_tls_handshake_rejections_total", "TLS handshakes refused, by KEEL-PROTO code.")
	m.protoRejections.writeTo(w, "keel_protocol_rejections_total", "Protocol rejections sent as 301 to the notice page, by KEEL-PROTO code.")
	m.protoNotices.writeTo(w, "keel_protocol_notices_served_total", "Protocol notice pages served, by KEEL-PROTO code and format.")
	fmt.Fprintf(w, "# HELP keel_http_inflight_requests Current in-flight HTTP requests.\n")
	fmt.Fprintf(w, "# TYPE keel_http_inflight_requests gauge\n")
	fmt.Fprintf(w, "keel_http_inflight_requests %g\n", m.inflight.get())
	fmt.Fprintf(w, "# HELP keel_fips_active 1 if FIPS mode is active, 0 otherwise.\n")
	fmt.Fprintf(w, "# TYPE keel_fips_active gauge\n")
	fmt.Fprintf(w, "keel_fips_active %g\n", fipsActive)
	fmt.Fprintf(w, "# HELP keel_tls_cert_expiry_seconds Seconds until TLS certificate expiry; negative means expired.\n")
	fmt.Fprintf(w, "# TYPE keel_tls_cert_expiry_seconds gauge\n")
	fmt.Fprintf(w, "keel_tls_cert_expiry_seconds %d\n", atomic.LoadInt64(&m.certExpirySecs))
	fmt.Fprintf(w, "# HELP keel_log_drops_total Cumulative remote-log lines dropped due to buffer overflow.\n")
	fmt.Fprintf(w, "# TYPE keel_log_drops_total counter\n")
	fmt.Fprintf(w, "keel_log_drops_total %d\n", atomic.LoadInt64(&m.logDrops))
	fmt.Fprintf(w, "# HELP keel_upstream_dial_retries_total Sidecar upstream connects that failed and were retried.\n")
	fmt.Fprintf(w, "# TYPE keel_upstream_dial_retries_total counter\n")
	fmt.Fprintf(w, "keel_upstream_dial_retries_total %d\n", atomic.LoadInt64(&m.dialRetries))
	fmt.Fprintf(w, "# HELP keel_upstream_dial_failures_total Sidecar requests whose every upstream connect failed.\n")
	fmt.Fprintf(w, "# TYPE keel_upstream_dial_failures_total counter\n")
	fmt.Fprintf(w, "keel_upstream_dial_failures_total %d\n", atomic.LoadInt64(&m.dialFailures))
	fmt.Fprintf(w, "# HELP keel_upstream_retry_after_total Sidecar requests retried after an upstream Retry-After.\n")
	fmt.Fprintf(w, "# TYPE keel_upstream_retry_after_total counter\n")
	fmt.Fprintf(w, "keel_upstream_retry_after_total %d\n", atomic.LoadInt64(&m.retryAfters))
	if v := atomic.LoadInt64(&m.fipsMonitorFailures); v > 0 {
		fmt.Fprintf(w, "# HELP keel_fips_monitor_failures_total Cumulative FIPS monitor check failures detected by the background loop.\n")
		fmt.Fprintf(w, "# TYPE keel_fips_monitor_failures_total counter\n")
		fmt.Fprintf(w, "keel_fips_monitor_failures_total %d\n", v)
	}
	m.mu.Lock()
	cs := append([]Collector(nil), m.collectors...)
	m.mu.Unlock()
	for _, c := range cs {
		c.Collect(w)
	}
}

// statusCapture wraps http.ResponseWriter to capture the written status code.
type statusCapture struct {
	http.ResponseWriter
	status int
}

// Unwrap exposes the wrapped writer so http.ResponseController can reach the connection's Flusher —
// required for live streaming (SSE) passthrough through this middleware.
func (sc *statusCapture) Unwrap() http.ResponseWriter { return sc.ResponseWriter }

func (sc *statusCapture) WriteHeader(code int) {
	sc.status = code
	sc.ResponseWriter.WriteHeader(code)
}
