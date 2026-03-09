//go:build !no_prom

package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
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
	requests       *labelCounter
	inflight       inflightGauge
	certExpirySecs int64 // atomic; seconds until TLS cert expiry
	logDrops       int64 // atomic; cumulative remote-log drop count
}

// New creates a zeroed Metrics instance.
func New() *Metrics {
	return &Metrics{requests: newLabelCounter()}
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
		labels := fmt.Sprintf(`method="%s",status="%d"`, r.Method, sc.status)
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

func (m *Metrics) writeTo(w io.Writer) {
	m.requests.writeTo(w, "keel_requests_total", "Total HTTP requests processed.")
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
}

// statusCapture wraps http.ResponseWriter to capture the written status code.
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.status = code
	sc.ResponseWriter.WriteHeader(code)
}
