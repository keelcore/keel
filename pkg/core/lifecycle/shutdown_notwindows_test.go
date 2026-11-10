//go:build !windows

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

// WaitForStop returns context.Canceled when a signal arrives on the channel.
// Pre-send SIGTERM before calling WaitForStop so the buffered channel is ready.
func TestWaitForStop_SignalBranch(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := NewShutdownOrchestrator(log)

	// The channel has capacity 2; pre-sending the signal ensures it is queued
	// before WaitForStop blocks on the select.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Skipf("cannot send SIGTERM to self: %v", err)
	}
	time.Sleep(5 * time.Millisecond) // let the OS deliver the signal

	err := o.WaitForStop(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled from signal branch, got %v", err)
	}
}
