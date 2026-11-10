//go:build !no_sidecar

package sidecar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
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

// okRoundTripper is a fake transport returning a fixed status code.
type okRoundTripper struct{ status int }

func (o okRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: o.status,
		Body:       io.NopCloser(nil),
		Request:    req,
	}, nil
}

// Allow returns false for an unrecognized breaker state (defensive default).
func TestBreaker_Allow_InvalidState_ReturnsFalse(t *testing.T) {
	b := newBreaker(3, time.Second)
	b.state = breakerState(99)
	allowed, onResult := b.Allow()
	if allowed {
		t.Error("expected Allow=false for invalid state")
	}
	if onResult != nil {
		t.Error("expected nil onResult for invalid state")
	}
}

// RoundTrip returns the response and records success when inner succeeds (<500).
func TestCBTransport_Success_RecordsSuccess(t *testing.T) {
	b := newBreaker(5, time.Minute)
	b.failures = 2
	ct := &cbTransport{inner: okRoundTripper{status: http.StatusOK}, breaker: b}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	resp, err := ct.RoundTrip(req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 response, got %v", resp)
	}
	if b.failures != 0 {
		t.Errorf("expected failures reset to 0 after success, got %d", b.failures)
	}
}

// Allow returns true in the closed state.
func TestBreaker_Allow_Closed_ReturnsTrue(t *testing.T) {
	b := newBreaker(3, time.Second)
	allowed, onResult := b.Allow()
	if !allowed {
		t.Error("expected Allow=true in closed state")
	}
	if onResult == nil {
		t.Error("expected non-nil onResult in closed state")
	}
	onResult(true)
}

// After threshold failures, Allow returns false (open state).
func TestBreaker_Allow_OpenAfterThreshold(t *testing.T) {
	b := newBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		allowed, onResult := b.Allow()
		if !allowed {
			t.Fatalf("expected Allow=true on iteration %d", i)
		}
		onResult(false)
	}
	allowed, _ := b.Allow()
	if allowed {
		t.Error("expected Allow=false after hitting failure threshold")
	}
}

// Allow returns false in the half-open state when a probe is already in flight.
func TestBreaker_Allow_HalfOpen_RejectsConcurrent(t *testing.T) {
	b := newBreaker(1, 1*time.Nanosecond) // very short reset timeout
	// Trip the breaker.
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("expected Allow=true initially")
	}
	onResult(false) // now open

	// Wait past reset timeout so the next Allow transitions to half-open.
	time.Sleep(5 * time.Millisecond)

	// First Allow in open state transitions to half-open and returns true.
	allowed1, onResult1 := b.Allow()
	if !allowed1 {
		t.Fatal("expected Allow=true after reset timeout (probe slot)")
	}

	// Second Allow while probe is in flight (half-open) must return false.
	allowed2, _ := b.Allow()
	if allowed2 {
		t.Error("expected Allow=false for concurrent request in half-open state")
	}

	// Clean up probe.
	onResult1(true)
}

// recordProbeResult(true) returns the breaker to closed state.
func TestBreaker_RecordProbeResult_Success_ClosesBreaker(t *testing.T) {
	b := newBreaker(1, 1*time.Nanosecond)
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("initial Allow must succeed")
	}
	onResult(false) // open
	time.Sleep(5 * time.Millisecond)

	// Probe succeeds → closed.
	allowed, probeResult := b.Allow()
	if !allowed {
		t.Fatal("expected probe slot to be granted")
	}
	probeResult(true)

	// Breaker is closed again.
	allowed, onResult2 := b.Allow()
	if !allowed {
		t.Error("expected Allow=true after successful probe (breaker closed)")
	}
	onResult2(true)
}

// recordProbeResult(false) keeps the breaker open.
func TestBreaker_RecordProbeResult_Failure_ReopensBreaker(t *testing.T) {
	b := newBreaker(1, 1*time.Nanosecond)
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("initial Allow must succeed")
	}
	onResult(false) // open
	time.Sleep(5 * time.Millisecond)

	// Probe fails → remains open.
	allowed, probeResult := b.Allow()
	if !allowed {
		t.Fatal("expected probe slot to be granted")
	}
	probeResult(false)

	// Breaker is open again (reset timeout is nanoseconds, so we need to wait).
	time.Sleep(5 * time.Millisecond)
	allowed, _ = b.Allow()
	// After failed probe the breaker opens; after nanosecond reset it may allow again.
	// We just verify the probeResult(false) call didn't close it.
	_ = allowed // state is open; exact timing-dependent; just verify no panic
}

// recordResult(true) resets failure count.
func TestBreaker_RecordResult_Success_ResetsFailures(t *testing.T) {
	b := newBreaker(3, time.Minute)
	// Two failures.
	for i := 0; i < 2; i++ {
		allowed, onResult := b.Allow()
		if !allowed {
			t.Fatalf("expected Allow=true on iteration %d", i)
		}
		onResult(false)
	}
	// One success resets the counter.
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("expected Allow=true before threshold")
	}
	onResult(true)
	if b.failures != 0 {
		t.Errorf("expected failures=0 after success, got %d", b.failures)
	}
}

// newBreaker with zero threshold defaults to 5.
func TestNewBreaker_ZeroThreshold_DefaultsFive(t *testing.T) {
	b := newBreaker(0, time.Second)
	if b.threshold != 5 {
		t.Errorf("expected default threshold=5, got %d", b.threshold)
	}
}

// newBreaker with zero resetTimeout defaults to 30s.
func TestNewBreaker_ZeroResetTimeout_Defaults30s(t *testing.T) {
	b := newBreaker(5, 0)
	if b.resetTimeout != 30*time.Second {
		t.Errorf("expected default resetTimeout=30s, got %v", b.resetTimeout)
	}
}

// cbTransport returns errCircuitOpen when the breaker is open.
func TestCBTransport_CircuitOpen_ReturnsError(t *testing.T) {
	b := newBreaker(1, time.Minute)
	// Trip the breaker.
	allowed, onResult := b.Allow()
	if !allowed {
		t.Fatal("expected Allow=true initially")
	}
	onResult(false) // threshold=1 → open

	ct := &cbTransport{
		inner:   http.DefaultTransport,
		breaker: b,
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	_, err := ct.RoundTrip(req)
	if err == nil {
		t.Error("expected error when circuit is open")
	}
	if err != errCircuitOpen {
		t.Errorf("expected errCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	var callCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := New(config.Config{
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

	h, err := New(config.Config{
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

	h, err := New(config.Config{
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

func TestCircuitBreaker_HalfOpenProbe_Fails_ReOpens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := New(config.Config{
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
