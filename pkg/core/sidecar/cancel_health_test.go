//go:build !no_sidecar

package sidecar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// ctxRoundTripper fails the way a real transport does when the inbound
// request's context is cancelled.
type ctxRoundTripper struct{}

func (ctxRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func cancelledRequest() *http.Request {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil).WithContext(ctx)
}

// Clients aborting requests must not open the breaker for everyone.
func TestCBTransport_ClientCancelDoesNotCount(t *testing.T) {
	b := newBreaker(3, time.Minute)
	ct := &cbTransport{inner: ctxRoundTripper{}, breaker: b}
	for i := 0; i < 10; i++ {
		if _, err := ct.RoundTrip(cancelledRequest()); err == nil {
			t.Fatal("expected the cancellation error")
		}
	}
	if b.state != breakerClosed || b.failures != 0 {
		t.Fatalf("state=%v failures=%d after client cancels, want closed/0", b.state, b.failures)
	}
}

// An abandoned half-open probe leaves the breaker open but immediately eligible
// to probe again — it is neither closed nor re-armed for a full reset timeout.
func TestCBTransport_AbandonedProbeReopensEligible(t *testing.T) {
	b := newBreaker(1, time.Minute)
	b.state = breakerOpen
	b.openedAt = time.Now().Add(-2 * time.Minute)
	ct := &cbTransport{inner: ctxRoundTripper{}, breaker: b}
	if _, err := ct.RoundTrip(cancelledRequest()); err == nil {
		t.Fatal("expected the cancellation error")
	}
	if b.state != breakerOpen {
		t.Fatalf("state = %v, want open", b.state)
	}
	if allowed, _ := b.Allow(); !allowed {
		t.Fatal("next request was not allowed to probe again")
	}
}

func TestAbandonProbe_NoOpWhenNotHalfOpen(t *testing.T) {
	b := newBreaker(1, time.Minute)
	b.abandonProbe()
	if b.state != breakerClosed {
		t.Fatalf("state = %v, want closed", b.state)
	}
}

// With no explicit transport the probe uses the upstream TLS settings; an
// unusable TLS config marks the sidecar not ready instead of probing without it.
func TestStartHealthProbe_UpstreamTLSError(t *testing.T) {
	r := probes.NewReadiness()
	cfg := config.SidecarConfig{
		UpstreamURL: "https://upstream.invalid",
		UpstreamTLS: config.UpstreamTLSConfig{Enabled: true, CAFile: "/nonexistent/ca.pem"},
	}
	StartHealthProbe(context.Background(), cfg, nil, r, logging.New(logging.Config{Out: io.Discard}))
	if ok, _ := r.IsReady(); ok {
		t.Fatal("readiness true despite an unusable upstream TLS config")
	}
}

// With no explicit transport and TLS disabled, the probe still works.
func TestStartHealthProbe_NilTransportProbes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(upstream.Close)
	r := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	StartHealthProbe(ctx, config.SidecarConfig{UpstreamURL: upstream.URL, UpstreamHealthPath: "/"}, nil, r, nil)
	deadline := time.Now().Add(3 * time.Second)
	for ok, _ := r.IsReady(); !ok && time.Now().Before(deadline); ok, _ = r.IsReady() {
		time.Sleep(20 * time.Millisecond)
	}
	if ok, _ := r.IsReady(); !ok {
		t.Fatal("probe over the built transport never reported ready")
	}
}

// The upstream signal is its own flag, aggregated by /readyz: with the upstream
// down the sidecar is not ready ("upstream" failing), while the backpressure
// flag the shedding middleware reads is untouched (no load shedding).
func TestStartHealthProbe_UpstreamSeparateFromBackpressure(t *testing.T) {
	r := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	StartHealthProbe(ctx, config.SidecarConfig{UpstreamURL: "http://127.0.0.1:1", UpstreamHealthPath: "/health",
		UpstreamHealthTimeout: config.DurationOf(50 * time.Millisecond)}, nil, r, nil)
	time.Sleep(150 * time.Millisecond)
	ok, failing := r.IsReady()
	if ok || len(failing) != 1 || !strings.HasPrefix(failing[0], "upstream") {
		t.Fatalf("IsReady = %v %v, want not ready with upstream failing", ok, failing)
	}
	if !r.Get() {
		t.Fatal("upstream failure cleared the backpressure flag (would shed all traffic)")
	}
}
