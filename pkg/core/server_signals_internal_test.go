//go:build !windows

package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// dumpConfig serialises the current config as JSON to os.Stderr.
// We redirect stderr to /dev/null to keep test output clean.
func TestDumpConfig_NoPanic(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = devNull
	defer func() {
		os.Stderr = old
		devNull.Close()
	}()

	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.dumpConfig() // must not panic
}

// threadSafeBuf is a goroutine-safe bytes.Buffer for signal-handler output.
type threadSafeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runSignalLoop SIGUSR2 branch: logs "sigusr2_received".
func TestSignalLoop_SIGUSR2_Logs(t *testing.T) {
	tb := &threadSafeBuf{}
	log := logging.New(logging.Config{Out: tb})
	s := NewServer(log, config.Config{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSignalLoop(ctx)
	}()

	time.Sleep(15 * time.Millisecond) // let goroutine register signal handlers

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR2); err != nil {
		cancel()
		<-done
		t.Skipf("cannot send SIGUSR2: %v", err)
	}
	time.Sleep(30 * time.Millisecond) // let signal be handled

	cancel()
	<-done

	if !strings.Contains(tb.String(), "sigusr2_received") {
		t.Errorf("expected 'sigusr2_received' in log output, got: %s", tb.String())
	}
}

// runSignalLoop SIGHUP branch: calls Reload (graceful failure with empty paths).
func TestSignalLoop_SIGHUP_TriggersReload(t *testing.T) {
	tb := &threadSafeBuf{}
	log := logging.New(logging.Config{Out: tb})
	// cfgPaths[0] = "" → Load("","") succeeds (uses defaults).
	s := NewServer(log, config.Config{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSignalLoop(ctx)
	}()

	time.Sleep(15 * time.Millisecond)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		cancel()
		<-done
		t.Skipf("cannot send SIGHUP: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	cancel()
	<-done

	// A successful Reload logs "config_reloaded".
	if !strings.Contains(tb.String(), "config_reloaded") {
		t.Errorf("expected 'config_reloaded' in log output, got: %s", tb.String())
	}
}

// runSignalLoop SIGUSR1 branch: calls dumpConfig (writes JSON to stderr).
func TestSignalLoop_SIGUSR1_DumpConfig(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = devNull
	defer func() {
		os.Stderr = old
		devNull.Close()
	}()

	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSignalLoop(ctx)
	}()

	time.Sleep(15 * time.Millisecond)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
		cancel()
		<-done
		t.Skipf("cannot send SIGUSR1: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	cancel()
	<-done
}