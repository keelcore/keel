// tests/unit/mw_gaps_test.go
package unit

import (
	"context"
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

// ---------------------------------------------------------------------------
// clientIP (via AccessLog middleware)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// OWASP: read timeout context
// ---------------------------------------------------------------------------
