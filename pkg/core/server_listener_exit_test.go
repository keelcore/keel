package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
)

// A listener that exits early (e.g. bind failure) ends the wait immediately —
// no signal needed — and its error stays on errCh for drainListeners to report.
func TestWaitForStop_ListenerExitStopsWait(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	errCh := make(chan error, 2)
	bindErr := errors.New("listen :8443: address already in use")
	errCh <- bindErr

	done := make(chan error, 1)
	go func() { done <- s.waitForStop(context.Background(), lifecycle.NewShutdownOrchestrator(log), errCh) }()
	select {
	case sigErr := <-done:
		if sigErr != nil {
			t.Fatalf("sigErr = %v, want nil on listener exit", sigErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForStop did not return when a listener exited")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, bindErr) {
			t.Fatalf("errCh = %v, want the listener error re-queued", err)
		}
	default:
		t.Fatal("listener error was not re-queued for drainListeners")
	}
}

// With no listener exit, ctx cancellation ends the wait with ctx's error.
func TestWaitForStop_ContextCancelled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.waitForStop(ctx, lifecycle.NewShutdownOrchestrator(log), make(chan error, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A clean nil from one listener must not mask a real error from another,
// whatever order they arrive in.
func TestDrainListeners_NilBeforeErrorStillFatal(t *testing.T) {
	s := NewServer(fatalLogger(), config.Config{})
	errCh := make(chan error, 2)
	errCh <- nil
	errCh <- errors.New("tls listener boom")
	expectFatal(t, func() {
		s.drainListeners(func() {}, &sync.WaitGroup{}, errCh, nil)
	})
}

// A failure is recorded but not obeyed until every listener has been attempted:
// with one listener still binding, waitForStop keeps waiting; once it binds,
// the recorded failure is obeyed and handed back on errCh.
func TestWaitForStop_ObeysFailureOnlyAfterAllAttempted(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	s.addListener() // fails
	s.addListener() // still binding
	errCh := make(chan error, 2)
	bindErr := errors.New("listen :9091: address already in use")
	errCh <- bindErr

	done := make(chan error, 1)
	go func() { done <- s.waitForStop(context.Background(), lifecycle.NewShutdownOrchestrator(log), errCh) }()
	select {
	case <-done:
		t.Fatal("waitForStop obeyed the failure before every listener was attempted")
	case <-time.After(200 * time.Millisecond):
	}
	s.listenerBound()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("waitForStop did not return once all listeners were attempted")
	}
	if err := <-errCh; !errors.Is(err, bindErr) {
		t.Fatalf("errCh = %v, want the recorded failure", err)
	}
}

// Every failure is reported in the exit, not just the first.
func TestWaitForStopAndDrain_AggregatesAllFailures(t *testing.T) {
	var out bytes.Buffer
	log := logging.New(logging.Config{Out: &out})
	log.ExitFn = func(int) { panic(errFatalSentinel) }
	s := NewServer(log, config.Config{})
	s.addListener()
	s.addListener()
	errCh := make(chan error, 2)
	errCh <- errors.New("listen :8080: address already in use")
	errCh <- errors.New("listen :8443: permission denied")

	if err := s.waitForStop(context.Background(), lifecycle.NewShutdownOrchestrator(log), errCh); err != nil {
		t.Fatalf("waitForStop = %v, want nil", err)
	}
	expectFatal(t, func() { s.drainListeners(func() {}, &sync.WaitGroup{}, errCh, nil) })
	for _, want := range []string{":8080: address already in use", ":8443: permission denied"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("exit error missing %q: %s", want, out.String())
		}
	}
}

// A listener that returns nil before shutdown counts as a failure.
func TestWaitForStop_UnexpectedNilExitIsFailure(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	s.addListener()
	errCh := make(chan error, 1)
	errCh <- nil
	_ = s.waitForStop(context.Background(), lifecycle.NewShutdownOrchestrator(log), errCh)
	if err := <-errCh; !errors.Is(err, errListenerExited) {
		t.Fatalf("errCh = %v, want errListenerExited", err)
	}
}

// A shutdown during collection still hands recorded failures to drainListeners.
func TestWaitForStop_SignalDuringCollectionRequeues(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	s.addListener()
	s.addListener()
	errCh := make(chan error, 2)
	bindErr := errors.New("bind boom")
	errCh <- bindErr
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if err := s.waitForStop(ctx, lifecycle.NewShutdownOrchestrator(log), errCh); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForStop = %v, want context.Canceled", err)
	}
	if err := <-errCh; !errors.Is(err, bindErr) {
		t.Fatalf("errCh = %v, want the recorded failure", err)
	}
}

// On shutdown, a listener's normal nil return can win the select against the
// ctx-driven WaitForStop. It must be treated as shutdown, not a failure.
func TestWaitForStop_ListenerReturnDuringShutdownIsNotFailure(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	s.addListener()
	errCh := make(chan error, 1)
	errCh <- nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 50; i++ { // both select cases are ready; exercise the race
		if err := s.waitForStop(ctx, lifecycle.NewShutdownOrchestrator(log), errCh); !errors.Is(err, context.Canceled) {
			t.Fatalf("waitForStop = %v, want context.Canceled", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("shutdown nil was turned into failure %v", err)
			}
			errCh <- nil
		default:
			t.Fatal("listener result lost")
		}
	}
}

// Binds that arrive while a wake-up is already pending must not block: the
// waiter re-reads the counters, so one pending signal is enough.
func TestListenerBound_DoesNotBlockWhenSignalPending(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.addListener()
	s.addListener()
	done := make(chan struct{})
	go func() {
		s.listenerBound()
		s.listenerBound() // signal already pending → non-blocking default path
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("listenerBound blocked with a wake-up already pending")
	}
	if got := s.lnBound.Load(); got != 2 {
		t.Fatalf("lnBound = %d, want 2", got)
	}
}
