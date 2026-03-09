// tests/unit/mw_gaps_test.go
package unit

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/probes"
)

// ---------------------------------------------------------------------------
// RunPressureLoop
// ---------------------------------------------------------------------------

func TestPressureLoop_EarlyReturn_HeapMaxBytesZero(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{} // HeapMaxBytes defaults to 0

	done := make(chan struct{})
	go func() {
		mw.RunPressureLoop(context.Background(), r, cfg, log)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("RunPressureLoop did not return early when HeapMaxBytes=0")
	}
}

func TestPressureLoop_ExitsOnContextCancel(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40, // 1 TiB — pressure near zero; covers in-range clamp01
			HighWatermark: 0.9,
			LowWatermark:  0.7,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mw.RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("RunPressureLoop did not exit on context cancel")
	}
}

// clamp01(v < 0): both watermarks below zero are clamped to 0.
func TestPressureLoop_ClampNegativeWatermarks(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40,
			HighWatermark: -0.1,
			LowWatermark:  -0.5,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the loop body runs; clamp happens before the select
	mw.RunPressureLoop(ctx, r, cfg, log)
}

// clamp01(v > 1): both watermarks above 1 are clamped to 1.
func TestPressureLoop_ClampAboveOneWatermarks(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40,
			HighWatermark: 1.5,
			LowWatermark:  1.2,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mw.RunPressureLoop(ctx, r, cfg, log)
}

// low > high after clamping: low is adjusted down to match high.
func TestPressureLoop_LowGreaterThanHighAdjusted(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40,
			HighWatermark: 0.3,
			LowWatermark:  0.8, // > high → adjusted to 0.3
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mw.RunPressureLoop(ctx, r, cfg, log)
}

// High watermark trigger: HeapMaxBytes=1 forces pressure >> any threshold,
// exercises the t.C case and the latch=true branch.
func TestPressureLoop_HighWatermarkTrigger(t *testing.T) {
	r := probes.NewReadiness()
	sb := &safeBuf{}
	log := logging.New(logging.Config{Out: sb})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1,   // 1 byte; HeapAlloc >> 1 → pressure > any threshold
			HighWatermark: 0.0, // clamped to 0; any positive pressure triggers latch
			LowWatermark:  -1,  // clamped to 0
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mw.RunPressureLoop(ctx, r, cfg, log)

	// Poll until the pressure latch fires (readiness → false) or 5 s deadline.
	// Fixed sleeps are unreliable under -race on slow CI runners; polling is not.
	deadline := time.Now().Add(5 * time.Second)
	for r.Get() && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond) // let goroutine exit before reading sb

	if r.Get() {
		t.Error("expected readiness false after high-pressure latch")
	}
	if !strings.Contains(sb.String(), "pressure_high") {
		t.Errorf("expected pressure_high log entry, got: %s", sb.String())
	}
}

// ---------------------------------------------------------------------------
// limitedResponseWriter.Write (via OWASP middleware with MaxResponseBodyBytes)
// ---------------------------------------------------------------------------

// Normal write: body fits within the limit.
func TestOWASP_ResponseBodyLimit_NormalWrite(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 100}}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if body := rr.Body.String(); body != "hi" {
		t.Errorf("expected %q, got %q", "hi", body)
	}
}

// Truncation: write larger than remaining budget.
func TestOWASP_ResponseBodyLimit_Truncates(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 5}}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world")) // 11 bytes → truncated to 5
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if body := rr.Body.String(); body != "hello" {
		t.Errorf("expected %q, got %q", "hello", body)
	}
}

// Second write when remaining==0: write is silently dropped.
func TestOWASP_ResponseBodyLimit_SecondWriteDropped(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 3}}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abc")) // consumes remaining=3 → remaining=0
		_, _ = w.Write([]byte("def")) // remaining==0 → dropped
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if body := rr.Body.String(); body != "abc" {
		t.Errorf("expected %q, got %q", "abc", body)
	}
}

// ---------------------------------------------------------------------------
// clientIP (via AccessLog middleware)
// ---------------------------------------------------------------------------

func TestAccessLog_ClientIP_XFF_WithComma(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	h := mw.AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "1.2.3.4, 5.6.7.8")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"ip":"1.2.3.4"`) {
		t.Errorf("expected first XFF entry in log, got: %s", buf.String())
	}
}

func TestAccessLog_ClientIP_XFF_WithoutComma(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	h := mw.AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "9.8.7.6")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"ip":"9.8.7.6"`) {
		t.Errorf("expected XFF IP in log, got: %s", buf.String())
	}
}

// Invalid RemoteAddr makes net.SplitHostPort fail; clientIP returns the raw value.
func TestAccessLog_ClientIP_InvalidRemoteAddr(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	h := mw.AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-a-valid-addr"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"ip":"not-a-valid-addr"`) {
		t.Errorf("expected raw RemoteAddr in log, got: %s", buf.String())
	}
}

// AccessLog logs req_id, trace_id, span_id when set by upstream middleware.
func TestAccessLog_LogsContextIDs(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	// Layer RequestID + TraceContext in front of AccessLog so all context keys are populated.
	h := mw.RequestID(mw.TraceContext(mw.AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	out := buf.String()
	if !strings.Contains(out, `"req_id"`) {
		t.Errorf("expected req_id in log, got: %s", out)
	}
	if !strings.Contains(out, `"trace_id"`) {
		t.Errorf("expected trace_id in log, got: %s", out)
	}
	if !strings.Contains(out, `"span_id"`) {
		t.Errorf("expected span_id in log, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// OWASP: read timeout context
// ---------------------------------------------------------------------------

func TestOWASP_ReadTimeout_SetsContext(t *testing.T) {
	cfg := config.Config{
		Timeouts: config.TimeoutsConfig{
			Read: config.DurationOf(time.Second),
		},
	}
	var deadlineSet bool
	h := mw.OWASP(cfg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, deadlineSet = r.Context().Deadline()
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !deadlineSet {
		t.Error("expected context deadline from OWASP read timeout")
	}
}
