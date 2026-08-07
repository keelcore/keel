// server_onbound_test.go — serveHTTPSBound and serveH3Bound fire onBound once the socket is bound.
package core

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
)

// TestServeHTTPS_OnBoundFires: serveHTTPS invokes onBound after the socket binds.
func TestServeHTTPS_OnBoundFires(t *testing.T) {
	orig := netListen
	netListen = func(_, _ string) (net.Listener, error) { return newGateListener(), nil }
	t.Cleanup(func() { netListen = orig })

	log := logging.New(logging.Config{Out: io.Discard})
	sd := lifecycle.NewShutdownOrchestrator(log)
	ctx, cancel := context.WithCancel(context.Background())

	var bound atomic.Bool
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPSBound(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), config.Config{}, nil, nil, log, func() { bound.Store(true) })
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !bound.Load() {
		if time.Now().After(deadline) {
			t.Fatal("onBound was never called after the HTTPS listener bound")
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("serveHTTPS did not exit after cancel")
	}
}

// TestServeH3_OnBoundFires: serveH3 invokes onBound (best-effort, before serving).
func TestServeH3_OnBoundFires(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var bound atomic.Bool
	_ = serveH3Bound(ctx, "127.0.0.1:0", http.NotFoundHandler(), config.Config{}, log, func() { bound.Store(true) })
	if !bound.Load() {
		t.Fatal("onBound was never called in serveH3")
	}
}
