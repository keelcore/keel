// server_loops2_test.go — additional tests for runCertExpiryLoop,
// runLogDropsLoop and runFIPSMonitorLoop (package core).
package core

import (
	"context"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
)

// runCertExpiryLoop: ticks at least once before ctx cancellation.
// Uses a very short ticker interval via the underlying time.Ticker default of 1h;
// we just verify the cancel path exits cleanly even if a tick never fires.
func TestRunCertExpiryLoop_CancelAfterStart(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Non-existent cert file: CertExpirySeconds errors silently.
	runCertExpiryLoop(ctx, "/nonexistent-cert-loop-test.pem", met)
}

// runLogDropsLoop: exits cleanly when ctx expires; nil getSink is not called.
func TestRunLogDropsLoop_NilSinkNoBlock(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runLogDropsLoop(ctx, func() *logging.HTTPSink { return nil }, met)
}

// runFIPSMonitorLoop: exits when ctx is cancelled.
func TestRunFIPSMonitorLoop_CancelAfterStart(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runFIPSMonitorLoop(ctx, met)
}
