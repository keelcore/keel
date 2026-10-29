package core

import (
	"io"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// Once draining, /readyz reports NOT ready — and a readiness Set(true) (as the
// sidecar health probe does each tick) cannot flip it back mid-drain.
func TestDrainingReadinessCheck(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.registerDrainingReadinessCheck()
	if ok, failing := s.readiness.IsReady(); !ok {
		t.Fatalf("ready before draining expected, failing=%v", failing)
	}
	s.draining.Store(true)
	s.readiness.Set(true)
	ok, failing := s.readiness.IsReady()
	if ok {
		t.Fatal("still ready while draining")
	}
	if len(failing) != 1 || failing[0] != "draining: shutting down" {
		t.Fatalf("failing = %v, want [draining: shutting down]", failing)
	}
}

// A reload arriving while startup holds reloadMu waits for startup to finish.
func TestReload_WaitsForStartup(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{},
		WithConfigPaths("/nonexistent/keel-reload-wait.yaml", ""))
	s.reloadMu.Lock() // as Run holds it during startup
	done := make(chan error, 1)
	go func() { done <- s.Reload() }()
	select {
	case <-done:
		t.Fatal("Reload ran while startup held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	s.reloadMu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Reload did not proceed after startup released the lock")
	}
}
