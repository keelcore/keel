//go:build !windows

package unit

import (
	"context"
	"errors"
	"io"
	"syscall"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
)

// WaitForStop returns context.Canceled when a signal arrives on the channel.
// Pre-send SIGTERM before calling WaitForStop so the buffered channel is ready.
func TestWaitForStop_SignalBranch(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := lifecycle.NewShutdownOrchestrator(log)

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

// WaitForStop returns ctx.Err when the context is cancelled.
func TestWaitForStop_CtxDone(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	o := lifecycle.NewShutdownOrchestrator(log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := o.WaitForStop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}