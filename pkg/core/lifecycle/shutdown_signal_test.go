//go:build !windows

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
