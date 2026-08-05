// pkg/core/lifecycle/shutdown_signal_test.go
package lifecycle

import (
	"context"
	"errors"
	"io"
	"syscall"
	"testing"

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
