package mw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// ---------------------------------------------------------------------------
// RequestID
// ---------------------------------------------------------------------------

// RequestID generates a ULID when x-request-id is absent.
func TestRequestID_GeneratesULID(t *testing.T) {
	var gotID string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("x-request-id")
	})
	h := RequestID(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if gotID != "" {
		t.Errorf("inner should not get x-request-id from inbound; got %q", gotID)
	}
	if rr.Header().Get("x-request-id") == "" {
		t.Error("expected x-request-id on response when absent from request")
	}
}

// RequestID propagates an existing x-request-id header.
func TestRequestID_PropagatesExisting(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-request-id", "my-id-123")
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("x-request-id"); got != "my-id-123" {
		t.Errorf("expected x-request-id=my-id-123 echoed, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// newULID
// ---------------------------------------------------------------------------

// newULID returns a 26-character string every time.
func TestNewULID_Length26(t *testing.T) {
	id := newULID()
	if len(id) != 26 {
		t.Errorf("expected ULID length 26, got %d (%q)", len(id), id)
	}
}

// newULID returns different values on consecutive calls.
func TestNewULID_Uniqueness(t *testing.T) {
	a := newULID()
	b := newULID()
	if a == b {
		t.Error("expected different ULIDs on consecutive calls")
	}
}

// ---------------------------------------------------------------------------
// AccessLog
// ---------------------------------------------------------------------------

// AccessLog logs and passes through the request; response code is captured.
func TestAccessLog_PassesThroughAndLogs(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})
	h := AccessLog(log, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// clientIP
// ---------------------------------------------------------------------------

// clientIP returns the first entry from X-Forwarded-For when present.
func TestClientIP_XFFSingleEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-forwarded-for", "1.2.3.4")
	got := clientIP(req)
	if got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %q", got)
	}
}

// clientIP returns the first entry of a comma-separated XFF.
func TestClientIP_XFFMultipleEntries(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-forwarded-for", "1.2.3.4, 5.6.7.8")
	got := clientIP(req)
	if got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4 (first XFF entry), got %q", got)
	}
}

// clientIP returns the remote addr IP when XFF is absent.
func TestClientIP_NoXFF_UsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.8.7.6:12345"
	got := clientIP(req)
	if got != "9.8.7.6" {
		t.Errorf("expected 9.8.7.6, got %q", got)
	}
}

// clientIP returns the raw remote addr when it has no port (not host:port).
func TestClientIP_RemoteAddrNoPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Del("x-forwarded-for")
	req.RemoteAddr = "192.168.1.1" // no port
	got := clientIP(req)
	if got != "192.168.1.1" {
		t.Errorf("expected raw addr 192.168.1.1, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Shedding
// ---------------------------------------------------------------------------

// Shedding passes through when readiness is OK.
func TestShedding_Ready_PassesThrough(t *testing.T) {
	r := probes.NewReadiness()
	r.Set(true)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := Shedding(r, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 when readiness=true, got %d", rr.Code)
	}
}

// Shedding returns 503 when readiness is false.
func TestShedding_NotReady_Returns503(t *testing.T) {
	r := probes.NewReadiness()
	r.Set(false)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := Shedding(r, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when readiness=false, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// ConcurrencyLimit
// ---------------------------------------------------------------------------

// ConcurrencyLimit with MaxConcurrent <= 0 is a no-op pass-through.
func TestConcurrencyLimit_Zero_Passthrough(t *testing.T) {
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 0}}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := ConcurrencyLimit(cfg, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from pass-through, got %d", rr.Code)
	}
}

// ConcurrencyLimit allows requests within the limit.
func TestConcurrencyLimit_WithinLimit_OK(t *testing.T) {
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 5, QueueDepth: 0}}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := ConcurrencyLimit(cfg, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 within limit, got %d", rr.Code)
	}
}

// ConcurrencyLimit returns 429 when at capacity and queue is full.
func TestConcurrencyLimit_AtCapacity_Returns429(t *testing.T) {
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 1, QueueDepth: 0}}
	// Block the single slot.
	ready := make(chan struct{})
	release := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(ready)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := ConcurrencyLimit(cfg, inner)

	// First request occupies the slot.
	go func() {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(rr, req)
	}()
	<-ready // first request is now in flight

	// Second request: slot full, queue depth=0 → 429.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	close(release)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 when at capacity, got %d", rr.Code)
	}
}

// ConcurrencyLimit returns 503 when request context is cancelled while queued.
func TestConcurrencyLimit_ContextCancelled_Returns503(t *testing.T) {
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 1, QueueDepth: 5}}
	// Block the single slot forever.
	release := make(chan struct{})
	ready := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(ready)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := ConcurrencyLimit(cfg, inner)

	// First request occupies the slot.
	go func() {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(rr, req)
	}()
	<-ready

	// Second request: queued but ctx cancelled immediately → 503.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	h.ServeHTTP(rr, req)

	close(release)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when context cancelled in queue, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// RunPressureLoop
// ---------------------------------------------------------------------------

// RunPressureLoop exits immediately when HeapMaxBytes <= 0.
func TestRunPressureLoop_ZeroHeapMax_ExitsImmediately(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{HeapMaxBytes: 0},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()

	select {
	case <-done:
		// good: exited immediately
	case <-time.After(500 * time.Millisecond):
		t.Error("RunPressureLoop did not exit immediately with HeapMaxBytes=0")
	}
}

// RunPressureLoop exits when context is cancelled.
func TestRunPressureLoop_ContextCancel_Exits(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40, // 1 TiB — effectively unreachable in tests
			HighWatermark: 0.90,
			LowWatermark:  0.70,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Error("RunPressureLoop did not exit after context cancel")
	}
}

// ---------------------------------------------------------------------------
// clamp01
// ---------------------------------------------------------------------------

func TestClamp01_BelowZero_ReturnsZero(t *testing.T) {
	if got := clamp01(-0.5); got != 0 {
		t.Errorf("clamp01(-0.5): expected 0, got %f", got)
	}
}

func TestClamp01_AboveOne_ReturnsOne(t *testing.T) {
	if got := clamp01(1.5); got != 1 {
		t.Errorf("clamp01(1.5): expected 1, got %f", got)
	}
}

func TestClamp01_InRange_ReturnsValue(t *testing.T) {
	if got := clamp01(0.5); got != 0.5 {
		t.Errorf("clamp01(0.5): expected 0.5, got %f", got)
	}
}
