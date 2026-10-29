// pkg/core/lifecycle/shutdown_signal_test.go
package lifecycle

import (
	"context"
	"errors"
	"io"
	"syscall"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// WaitForStop — signal branch
// ---------------------------------------------------------------------------

// WaitForStop returns context.Canceled and logs a warning when a signal is
// delivered on the internal channel. The buffered channel is injected directly
// (same package) so no real OS signal is required.
func TestWaitForStop_SignalReceived(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	// Push a signal into the buffered channel; the select must take the
	// signal case rather than blocking on the background context.
	o.ch <- syscall.SIGTERM

	err := o.WaitForStop(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled after signal, got %v", err)
	}
}

// After Stop, the orchestrator's channel no longer receives SIGTERM.
func TestOrchestrator_StopDetachesSignals(t *testing.T) {
	o := NewShutdownOrchestrator(logging.New(logging.Config{Out: io.Discard}))
	keep := NewShutdownOrchestrator(logging.New(logging.Config{Out: io.Discard})) // keeps SIGTERM from killing the test binary
	defer keep.Stop()
	o.Stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := keep.WaitForStop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("control orchestrator: %v", err)
	}
	select {
	case sig := <-o.ch:
		t.Fatalf("stopped orchestrator still received %v", sig)
	default:
	}
}
