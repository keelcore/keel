// pkg/core/lifecycle/shutdown_test.go
package lifecycle

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// GracefulStop
// ---------------------------------------------------------------------------

// GracefulStop with a successful fn must return nil.
func TestGracefulStop_SuccessfulFn(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	err := o.GracefulStop(time.Second, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// GracefulStop with a failing fn must propagate the error.
func TestGracefulStop_FnReturnsError(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	sentinel := errors.New("shutdown failed")
	err := o.GracefulStop(time.Second, func(_ context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// GracefulStop with zero timeout uses the 10-second default (fn completes fast).
func TestGracefulStop_ZeroTimeout_UsesDefault(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	err := o.GracefulStop(0, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("expected nil error with zero timeout, got %v", err)
	}
}

// GracefulStop: fn receives a context that is cancelled when the deadline is exceeded.
func TestGracefulStop_DeadlineExceeded(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	err := o.GracefulStop(10*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil {
		t.Error("expected context error when deadline exceeded")
	}
}

// ---------------------------------------------------------------------------
// WaitForStop — ctx-done branch (no signal needed)
// ---------------------------------------------------------------------------

// WaitForStop returns ctx.Err() when ctx is already cancelled.
func TestWaitForStop_CtxAlreadyCancelled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := o.WaitForStop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
