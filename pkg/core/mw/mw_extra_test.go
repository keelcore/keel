package mw

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// A multi-entry X-Forwarded-For is logged verbatim as "xff", never as "ip".
func TestClientIP_XFFMultipleEntries(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(logging.New(logging.Config{JSON: true, Out: &buf}), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.8.7.6:12345"
	req.Header.Set("x-forwarded-for", "1.2.3.4, 5.6.7.8")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(buf.String(), `"ip":"9.8.7.6"`) || !strings.Contains(buf.String(), `"xff":"1.2.3.4, 5.6.7.8"`) {
		t.Errorf("expected ip=peer and xff verbatim, got %s", buf.String())
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

// ---------------------------------------------------------------------------
// clamp01
// ---------------------------------------------------------------------------

// A valid client X-Request-ID is kept; an invalid or oversized one is replaced.
func TestRequestID_ValidatesInboundID(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cases := map[string]bool{
		"0b6c9a2e-7f1d-4c3a-9b8e-2d4f6a8c0e1f": true,
		"01JB4ZQ2W5Y8N6V3K7T9R1M0PX":           true,
		"evil\"id":                             false,
		"a b":                                  false,
		strings.Repeat("a", maxRequestIDLen+1): false,
	}
	for in, keep := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("x-request-id", in)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get("x-request-id")
		if keep && got != in {
			t.Errorf("valid %q replaced with %q", in, got)
		}
		if !keep && (got == in || len(got) != 26) {
			t.Errorf("invalid %q echoed as %q, want fresh ULID", in, got)
		}
	}
}
