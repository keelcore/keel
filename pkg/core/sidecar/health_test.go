//go:build !no_sidecar

package sidecar

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/tests/unit"
)

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

var errClose = errors.New("close failed")

type closeErrTransport struct{ status int }

func (t closeErrTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{}, Body: closeErrBody{strings.NewReader("ok")}}, nil
}

// A probe whose response body fails to close logs the failure; the status still decides health.
func TestDoProbe_CloseErrorLogged(t *testing.T) {
	var out bytes.Buffer
	h := &upstreamHealth{}
	client := &http.Client{Transport: closeErrTransport{status: http.StatusOK}}
	doProbe(client, "http://upstream.local/health", h, logging.New(logging.Config{Out: &out}))
	if !h.ok.Load() {
		t.Fatal("200 probe: upstream not healthy")
	}
	if !strings.Contains(out.String(), "upstream_health_probe_close_failed") || !strings.Contains(out.String(), errClose.Error()) {
		t.Fatalf("close error not logged: %q", out.String())
	}
}

// StartHealthProbe applies default timeout and interval when both are zero.
func TestStartHealthProbe_DefaultsApplied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	cfg := config.SidecarConfig{
		UpstreamURL:        upstream.URL,
		UpstreamHealthPath: "/health",
		// timeout and interval left at zero → defaults applied.
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartHealthProbe(ctx, cfg, nil, readiness, nil)
	time.Sleep(30 * time.Millisecond)
}

// StartHealthProbe fires the ticker at least once with a short interval.
func TestStartHealthProbe_TickerFires(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	cfg := config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(2 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(time.Second),
	}
	ctx, cancel := context.WithCancel(context.Background())
	StartHealthProbe(ctx, cfg, nil, readiness, nil)
	time.Sleep(40 * time.Millisecond)
	cancel()
	if hits.Load() < 2 {
		t.Errorf("expected ticker to fire multiple probes, got %d", hits.Load())
	}
}

// doProbe logs on connection error when a logger is set.
func TestDoProbe_ConnectionError_WithLog(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	h := &upstreamHealth{}
	h.ok.Store(true)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	doProbe(client, "http://127.0.0.1:1/health", h, log)
	if h.ok.Load() {
		t.Error("expected readiness=false on connection error")
	}
}

// doProbe logs an unhealthy status when a logger is set.
func TestDoProbe_Unhealthy_WithLog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	log := logging.New(logging.Config{Out: io.Discard})
	h := &upstreamHealth{}
	h.ok.Store(true)
	client := &http.Client{Timeout: time.Second}
	doProbe(client, upstream.URL+"/health", h, log)
	if h.ok.Load() {
		t.Error("expected readiness=false for unhealthy upstream")
	}
}

// StartHealthProbe starts a goroutine and does at least one probe without panic.
func TestStartHealthProbe_Runs_NoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	log := logging.New(logging.Config{Out: io.Discard})
	readiness := probes.NewReadiness()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.SidecarConfig{
		UpstreamURL:            srv.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(time.Hour),
		UpstreamHealthTimeout:  config.DurationOf(2 * time.Second),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartHealthProbe(ctx, cfg, nil, readiness, log)
	// Give the goroutine a moment to start and do at least one probe.
	time.Sleep(50 * time.Millisecond)
}

// doProbe marks the upstream healthy when it responds 200.
func TestDoProbe_Healthy_SetsReadinessTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := &upstreamHealth{}
	client := &http.Client{Timeout: 2 * time.Second}
	doProbe(client, srv.URL+"/health", h, nil)

	if !h.ok.Load() {
		t.Error("expected readiness=true for healthy upstream")
	}
}

// doProbe marks the upstream unhealthy when it responds 500.
func TestDoProbe_Unhealthy_SetsReadinessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := &upstreamHealth{}
	h.ok.Store(true)
	client := &http.Client{Timeout: 2 * time.Second}
	doProbe(client, srv.URL+"/health", h, nil)

	if h.ok.Load() {
		t.Error("expected readiness=false for unhealthy upstream (500)")
	}
}

// doProbe marks the upstream unhealthy on connection error.
func TestDoProbe_ConnectionError_SetsReadinessFalse(t *testing.T) {
	h := &upstreamHealth{}
	h.ok.Store(true)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	doProbe(client, "http://127.0.0.1:1/health", h, nil)

	if h.ok.Load() {
		t.Error("expected readiness=false on connection error")
	}
}

// doProbe with nil log still works (no-op on warning path).
func TestDoProbe_NilLog_NoUnhealthy_NoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	h := &upstreamHealth{}
	client := &http.Client{Timeout: 2 * time.Second}
	// Should not panic even with nil log and unhealthy status.
	doProbe(client, srv.URL+"/health", h, nil)
}

func TestHealthProbe_DefaultTimeouts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately; we just want the goroutine to start

	// Zero values for timeout and interval → defaults (2s and 10s) applied inside.
	StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:        upstream.URL,
		UpstreamHealthPath: "/health",
		// UpstreamHealthTimeout: zero → default 2s
		// UpstreamHealthInterval: zero → default 10s
	}, nil, readiness, nil)

	time.Sleep(20 * time.Millisecond) // let goroutine start and see ctx.Done
}

func TestHealthProbe_LogsOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	upstream.Close() // immediately down → probe returns err

	sb := &unit.SafeBuf{}
	log := logging.New(logging.Config{Out: sb})
	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())

	StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(5 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, log)

	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond) // let goroutine exit before reading buf

	if !strings.Contains(sb.String(), "upstream_health_probe_failed") {
		t.Errorf("expected upstream_health_probe_failed log, got: %s", sb.String())
	}
}

func TestHealthProbe_LogsOnUnhealthyStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503 → unhealthy
	}))
	defer upstream.Close()

	sb := &unit.SafeBuf{}
	log := logging.New(logging.Config{Out: sb})
	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())

	StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(5 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, log)

	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond) // let goroutine exit before reading buf

	if !strings.Contains(sb.String(), "upstream_unhealthy") {
		t.Errorf("expected upstream_unhealthy log, got: %s", sb.String())
	}
}

func TestSidecarHealthProbe_UpstreamDown_ReadinessFalse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	upstream.Close() // shut down immediately

	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(5 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, nil)

	time.Sleep(80 * time.Millisecond)

	if ok, _ := readiness.IsReady(); ok {
		t.Error("expected readiness false after upstream down")
	}
}

func TestSidecarHealthProbe_UpstreamRecovered_ReadinessTrue(t *testing.T) {
	var healthy atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(10 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, nil)

	time.Sleep(40 * time.Millisecond)
	if ok, _ := readiness.IsReady(); ok {
		t.Fatal("expected readiness false before upstream healthy")
	}

	healthy.Store(true)
	time.Sleep(40 * time.Millisecond)

	if ok, _ := readiness.IsReady(); !ok {
		t.Error("expected readiness true after upstream recovered")
	}
}
