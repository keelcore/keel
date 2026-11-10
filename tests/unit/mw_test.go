// tests/unit/mw_gaps_test.go
package unit

import (
	"context"
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
