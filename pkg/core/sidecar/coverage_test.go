//go:build !no_sidecar

package sidecar

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// circuit.go — unreachable default via invalid state
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// cbTransport.RoundTrip — success and inner-error paths
// ---------------------------------------------------------------------------

// RoundTrip returns the upstream error and records failure when inner fails.
func TestCBTransport_InnerError_RecordsFailure(t *testing.T) {
	b := newBreaker(5, time.Minute)
	boom := errors.New("boom")
	ct := &cbTransport{inner: errRoundTripper{err: boom}, breaker: b}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	_, err := ct.RoundTrip(req)
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
	if b.failures != 1 {
		t.Errorf("expected failures=1 after inner error, got %d", b.failures)
	}
}

// ---------------------------------------------------------------------------
// buildTransport — CA pool and client cert success paths, and the
// DefaultTransport-not-*http.Transport fallback.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// New — driving the ReverseProxy closures end-to-end
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// health.go — default timeout/interval, ticker tick, and log-set warn paths
// ---------------------------------------------------------------------------
