// server_readiness_bind_test.go — readiness must not report ready until the probe listeners
// (health, ready, admin, startup) have actually bound their sockets. k8s readiness means "ready to
// serve", so /readyz must gate on the listeners accepting connections, not merely on the process
// having spawned their goroutines.
package core

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

// TestServeHTTPBoundSignalsOnBind: serveHTTPBound invokes onBound AFTER the socket is bound (after
// net.Listen), so a caller can barrier on real bind completion.
func TestServeHTTPBoundSignalsOnBind(t *testing.T) {
	s := newStubServer(t, config.Defaults())
	sd := newStubSD(s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var bound atomic.Bool
	go func() {
		_ = serveHTTPBound(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), s.cfg, s.logger, func() { bound.Store(true) })
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !bound.Load() {
		if time.Now().After(deadline) {
			t.Fatal("onBound was never called after the listener bound")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestReadinessListenerGate: the "listeners" readiness check keeps /readyz not-ready until the
// listener bind barrier flips listenersServing, then reports ready.
func TestReadinessListenerGate(t *testing.T) {
	s := newStubServer(t, config.Defaults())
	s.registerListenerReadinessCheck()

	if ok, _ := s.readiness.IsReady(); ok {
		t.Fatal("readiness must be NOT ready before the listeners have bound")
	}
	s.listenersServing.Store(true)
	if ok, _ := s.readiness.IsReady(); !ok {
		t.Fatal("readiness must be ready once the listeners are serving")
	}
}
