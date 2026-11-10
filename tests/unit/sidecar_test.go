//go:build !no_sidecar

package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/pkg/core/sidecar"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Plain HTTP proxy
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Response size cap
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// XFF modes
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Header policy
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Upstream TLS
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Health probe
// ---------------------------------------------------------------------------

func TestSidecarHealthProbe_UpstreamDown_ReadinessFalse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	upstream.Close() // shut down immediately

	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
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

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
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

// ---------------------------------------------------------------------------
// Circuit breaker
// ---------------------------------------------------------------------------

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	var callCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(10 * time.Second),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First two requests reach upstream and record failures.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Third request: circuit is open, upstream must NOT be called.
	prevCount := callCount.Load()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 from open circuit, got %d", rr.Code)
	}
	if callCount.Load() != prevCount {
		t.Errorf("circuit should not call upstream when open (callCount was %d, now %d)", prevCount, callCount.Load())
	}
}

func TestCircuitBreaker_HalfOpenProbe_ClosesCircuit(t *testing.T) {
	var serving atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if serving.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(30 * time.Millisecond),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Trigger threshold failures → circuit opens.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Wait past reset timeout → circuit transitions to half-open.
	time.Sleep(50 * time.Millisecond)
	serving.Store(true)

	// Half-open probe succeeds → circuit closes.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("half-open probe: got %d, want 200", rr.Code)
	}

	// Subsequent request should also succeed (circuit is closed).
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("post-close request: got %d, want 200", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// newBreaker: default threshold and resetTimeout when zero values passed
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Allow: breakerHalfOpen rejection (concurrent request while probe is in flight)
// ---------------------------------------------------------------------------

func TestCircuitBreaker_HalfOpen_ConcurrentRejected(t *testing.T) {
	// Single upstream: first 2 requests return 500 to open the circuit; subsequent
	// requests sleep 80ms then return 200 so the half-open probe stays in-flight
	// long enough for a concurrent request to see the half-open state and be rejected.
	var n atomic.Int32
	probeStarted := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		select {
		case probeStarted <- struct{}{}:
		default:
		}
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(40 * time.Millisecond),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Two failures → circuit opens.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Wait past reset timeout → next Allow() transitions to half-open.
	time.Sleep(60 * time.Millisecond)

	// Goroutine sends the half-open probe (slow upstream).
	done := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
		done <- rr.Code
	}()

	// Wait until probe is in flight (upstream received the request).
	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not start within 2s")
	}

	// Second request: breaker is half-open → Allow() returns false → 502.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 from half-open rejection, got %d", rr.Code)
	}

	<-done // let goroutine finish
}

// ---------------------------------------------------------------------------
// recordProbeResult(false): half-open probe failure re-opens circuit
// ---------------------------------------------------------------------------

func TestCircuitBreaker_HalfOpenProbe_Fails_ReOpens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(30 * time.Millisecond),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Two failures → circuit opens.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Wait past reset timeout → half-open on next request.
	time.Sleep(50 * time.Millisecond)

	// Half-open probe: upstream still returns 500 → recordProbeResult(false) → re-open.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	// Response may be 500 (probe got through) — not 502 yet.

	// Immediately after: circuit is open again → 502.
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest("GET", "/", nil))
	if rr2.Code != http.StatusBadGateway {
		t.Errorf("expected 502 after re-open from failed half-open probe, got %d", rr2.Code)
	}
}

// ---------------------------------------------------------------------------
// RoundTrip: inner transport error (onResult(false) + return nil, err)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// doProbe: log != nil paths (warn on failure and on unhealthy status)
// ---------------------------------------------------------------------------

func TestHealthProbe_LogsOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	upstream.Close() // immediately down → probe returns err

	sb := &safeBuf{}
	log := logging.New(logging.Config{Out: sb})
	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
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

	sb := &safeBuf{}
	log := logging.New(logging.Config{Out: sb})
	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
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

// ---------------------------------------------------------------------------
// StartHealthProbe: default timeout and interval (zero values → defaults applied)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// New: invalid upstream URL → error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// New + ModifyResponse: maxResp <= 0 returns nil immediately
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildTransport: CA file with no cert blocks → error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildTransport: client cert + key load error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// applyHeaderPolicy: Forward allowlist removes headers not in the list
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// addrToIP: address with no port → SplitHostPort fails → raw address returned
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// XFF append: no incoming XFF (empty) → only client IP set
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// X-Real-IP
// ---------------------------------------------------------------------------
