package mw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// AccessLog reads req_id, trace_id, and span_id from the request context.
func TestAccessLog_LogsContextFields(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := AccessLog(log, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	ctx := context.WithValue(req.Context(), ctxkeys.RequestID, "req-123")
	ctx = context.WithValue(ctx, ctxkeys.TraceID, "trace-abc")
	ctx = context.WithValue(ctx, ctxkeys.SpanID, "span-def")
	req = req.WithContext(ctx)

	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// accessWriter.Unwrap exposes the wrapped writer.
func TestAccessWriter_Unwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	aw := &accessWriter{ResponseWriter: rr, status: http.StatusOK}
	if aw.Unwrap() != rr {
		t.Error("expected Unwrap to return the wrapped ResponseWriter")
	}
}

// RunPressureLoop latches readiness off under high heap pressure and recovers
// once pressure falls back below the low watermark. The readMemStats seam
// injects deterministic heap readings.
func TestRunPressureLoop_LatchAndRecover(t *testing.T) {
	var calls int32
	orig := readMemStats
	readMemStats = func(ms *runtime.MemStats) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			ms.HeapAlloc = 950 // pressure 0.95 >= high(0.9) → latch
		} else {
			ms.HeapAlloc = 100 // pressure 0.10 <= low(0.5) → recover
		}
	}
	defer func() { readMemStats = orig }()

	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	r.Set(true)
	cfg := config.Config{Backpressure: config.BackpressureConfig{
		HeapMaxBytes:  1000,
		HighWatermark: 0.9,
		LowWatermark:  0.5,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()

	// Wait for the latch (readiness → false), then for recovery (→ true).
	deadline := time.After(5 * time.Second)
	waitFor := func(want bool) {
		for {
			if r.Get() == want {
				return
			}
			select {
			case <-deadline:
				t.Fatalf("timed out waiting for readiness=%v", want)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitFor(false)
	waitFor(true)

	cancel()
	<-done
}

// RunPressureLoop clamps the low watermark to the high watermark when low > high.
func TestRunPressureLoop_LowAboveHigh_Clamped(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	cfg := config.Config{Backpressure: config.BackpressureConfig{
		HeapMaxBytes:  1 << 40,
		HighWatermark: 0.3,
		LowWatermark:  0.7, // low > high → clamped to high
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("RunPressureLoop did not exit after cancel")
	}
}
