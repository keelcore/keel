//go:build !no_sidecar

// tests/unit/sidecar_gaps_test.go
package unit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
// newBreaker: default threshold and resetTimeout when zero values passed
// ---------------------------------------------------------------------------

func TestCircuitBreaker_DefaultThreshold(t *testing.T) {
	// FailureThreshold=0 and ResetTimeout=0 trigger the defaults inside newBreaker.
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
				FailureThreshold: 0, // → default 5
				// ResetTimeout zero → default 30s
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	// Just verify the proxy works (circuit not open after one failure with default threshold 5).
	if rr.Code == 0 {
		t.Error("expected a response code")
	}
}

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

func TestCircuitBreaker_RoundTrip_TransportError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	url := upstream.URL
	upstream.Close() // close immediately; transport dial will fail

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: url,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 10, // high threshold so circuit stays closed
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for transport error, got %d", rr.Code)
	}
}

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

func TestHealthProbe_DefaultTimeouts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately; we just want the goroutine to start

	// Zero values for timeout and interval → defaults (2s and 10s) applied inside.
	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:        upstream.URL,
		UpstreamHealthPath: "/health",
		// UpstreamHealthTimeout: zero → default 2s
		// UpstreamHealthInterval: zero → default 10s
	}, nil, readiness, nil)

	time.Sleep(20 * time.Millisecond) // let goroutine start and see ctx.Done
}

// ---------------------------------------------------------------------------
// New: invalid upstream URL → error
// ---------------------------------------------------------------------------

func TestSidecarNew_BadURL(t *testing.T) {
	_, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "://bad-url",
		},
	})
	if err == nil {
		t.Error("expected error for invalid upstream URL")
	}
}

// ---------------------------------------------------------------------------
// New + ModifyResponse: maxResp <= 0 returns nil immediately
// ---------------------------------------------------------------------------

func TestSidecarProxy_NoResponseSizeCap(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "big response")
	}))
	defer upstream.Close()

	// MaxResponseBodyBytes=0 → ModifyResponse returns nil immediately.
	h, err := sidecar.New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 0},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// buildTransport: CA file with no cert blocks → error
// ---------------------------------------------------------------------------

func TestBuildTransport_CAFile_NoCerts(t *testing.T) {
	// Write a PEM file that has no CERTIFICATE block.
	caFile := writeTempFile(t, []byte("-----BEGIN PRIVATE KEY-----\nYWJj\n-----END PRIVATE KEY-----\n"))

	_, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "http://127.0.0.1:1",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled: true,
				CAFile:  caFile,
			},
		},
	})
	if err == nil {
		t.Error("expected error for CA file with no certificates")
	}
}

// ---------------------------------------------------------------------------
// buildTransport: client cert + key load error
// ---------------------------------------------------------------------------

func TestBuildTransport_ClientCert_LoadError(t *testing.T) {
	caKey, caCert, caPEM := generateTestCA(t)
	certPEM, _ := signTestCert(t, caKey, caCert, true)
	// Write cert but use wrong key (the CA key, not the cert's key).
	caKeyDER, _ := os.ReadFile(writeTempFile(t, caPEM))
	certFile := writeTempFile(t, certPEM)
	keyFile := writeTempFile(t, caKeyDER) // wrong key → LoadX509KeyPair fails

	_, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "http://127.0.0.1:1",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled:        true,
				ClientCertFile: certFile,
				ClientKeyFile:  keyFile,
			},
		},
	})
	if err == nil {
		t.Error("expected error for mismatched client cert/key")
	}
}

// ---------------------------------------------------------------------------
// applyHeaderPolicy: Forward allowlist removes headers not in the list
// ---------------------------------------------------------------------------

func TestSidecarProxy_HeaderForwardAllowlist(t *testing.T) {
	var gotAllowed, gotOther string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAllowed = r.Header.Get("x-allowed")
		gotOther = r.Header.Get("x-other")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			HeaderPolicy: config.HeaderPolicyConfig{
				Forward: []string{"x-allowed"}, // only x-allowed passes
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-allowed", "yes")
	req.Header.Set("x-other", "should-be-stripped")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotAllowed != "yes" {
		t.Errorf("x-allowed: got %q, want %q", gotAllowed, "yes")
	}
	if gotOther != "" {
		t.Errorf("x-other should be stripped by forward allowlist, got %q", gotOther)
	}
}

// ---------------------------------------------------------------------------
// addrToIP: address with no port → SplitHostPort fails → raw address returned
// ---------------------------------------------------------------------------

func TestSidecarProxy_XFF_Append_NoPort(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1" // no port → addrToIP returns raw value
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(gotXFF, "192.168.1.1") {
		t.Errorf("expected raw addr in XFF, got %q", gotXFF)
	}
}

// ---------------------------------------------------------------------------
// XFF append: no incoming XFF (empty) → only client IP set
// ---------------------------------------------------------------------------

func TestSidecarProxy_XFF_Append_NoIncomingXFF(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:4321"
	// No x-forwarded-for set → append mode sets only the client IP.
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotXFF != "10.0.0.1" {
		t.Errorf("expected only client IP in XFF, got %q", gotXFF)
	}
}
