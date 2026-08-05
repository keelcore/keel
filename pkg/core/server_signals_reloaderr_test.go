//go:build !windows

package core

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// runSignalLoop SIGHUP branch where Reload fails: cfgPaths[0] points at a
// directory, so config.Load returns an error and the handler logs
// "sighup_reload_failed" instead of "config_reloaded".
func TestSignalLoop_SIGHUP_ReloadError(t *testing.T) {
	tb := &threadSafeBuf{}
	log := logging.New(logging.Config{Out: tb})
	s := NewServer(log, config.Config{})
	// A directory path makes os.ReadFile (inside config.Load) fail.
	s.cfgPaths[0] = t.TempDir()

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
	time.Sleep(40 * time.Millisecond)

	cancel()
	<-done

	if got := tb.String(); !strings.Contains(got, "sighup_reload_failed") {
		t.Errorf("expected 'sighup_reload_failed' in log output, got: %s", got)
	}
}
