// server_readiness_gate_test.go — /readyz must report not-ready until every enabled listener (probe,
// main HTTP, and embedder-registered) has bound its socket. These tests drive Run() with the
// netListen seam replaced by a controllable fake, so a listener's bind can be delayed on demand.
package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// Controllable listen seam
// ---------------------------------------------------------------------------

// gateListener is a net.Listener whose Accept blocks until Close, so a faked
// listener stays "up" (never busy-loops, never errors early) until graceful
// shutdown closes it — at which point http.Server.Serve returns ErrServerClosed.
type gateListener struct {
	closed chan struct{}
	once   sync.Once
}

func newGateListener() *gateListener { return &gateListener{closed: make(chan struct{})} }

func (l *gateListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, errors.New("gateListener closed")
}

func (l *gateListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *gateListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// listenGate replaces the package netListen seam. For a "held" addr, netListen
// blocks (simulating a slow bind that has not completed) until the addr is
// released; every other addr binds immediately to a gateListener. No real
// sockets are opened, so tests are free of port conflicts.
type listenGate struct {
	mu       sync.Mutex
	held     map[string]chan struct{}
	released map[string]bool
}

func installListenGate(t *testing.T, holdAddrs ...string) *listenGate {
	t.Helper()
	g := &listenGate{held: map[string]chan struct{}{}, released: map[string]bool{}}
	for _, a := range holdAddrs {
		g.held[a] = make(chan struct{})
	}
	orig := netListen
	netListen = func(_, addr string) (net.Listener, error) {
		g.mu.Lock()
		ch := g.held[addr]
		g.mu.Unlock()
		if ch != nil {
			<-ch // block until released
		}
		return newGateListener(), nil
	}
	t.Cleanup(func() {
		netListen = orig
		g.releaseAll()
	})
	return g
}

func (g *listenGate) release(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ch, ok := g.held[addr]; ok && !g.released[addr] {
		g.released[addr] = true
		close(ch)
	}
}

func (g *listenGate) releaseAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for addr, ch := range g.held {
		if !g.released[addr] {
			g.released[addr] = true
			close(ch)
		}
	}
}

// ---------------------------------------------------------------------------
// Run harness + readiness polling
// ---------------------------------------------------------------------------

// runInBackground starts s.Run(ctx) in a goroutine. Cleanup releases any held
// binds (so the serve goroutines can progress past netListen), cancels, then
// waits (bounded) for Run to return — order matters, else Run's wg.Wait blocks
// on a listener still parked in the faked netListen.
func runInBackground(t *testing.T, s *Server, gate *listenGate) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		gate.releaseAll()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("Run did not return within 5s of cancel")
		}
	})
}

func isReady(s *Server) bool {
	ok, _ := s.readiness.IsReady()
	return ok
}

// waitReadyState polls until IsReady == want, or the deadline elapses.
func waitReadyState(s *Server, want bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if isReady(s) == want {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// assertStaysNotReady fails if /readyz becomes ready at any point during d.
func assertStaysNotReady(t *testing.T, s *Server, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if isReady(s) {
			t.Fatalf("/readyz reported READY while a listener bind was still outstanding")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func gateTestLogger() *logging.Logger { return logging.New(logging.Config{Out: io.Discard}) }

// ---------------------------------------------------------------------------
// readyz gates on a probe listener's bind
// ---------------------------------------------------------------------------

func TestReadyz_GatesOnProbeListenerBind(t *testing.T) {
	cfg := config.Config{}
	cfg.Listeners.Ready.Enabled = true
	cfg.Listeners.Ready.Port = 19092
	readyAddr := config.AddrFromPort(19092)

	gate := installListenGate(t, readyAddr)
	s := NewServer(gateTestLogger(), cfg)
	runInBackground(t, s, gate)

	if !waitReadyState(s, false, 2*time.Second) {
		t.Fatal("/readyz never reported not-ready during the bind window: the listener readiness gate is not wired into Run")
	}
	assertStaysNotReady(t, s, 200*time.Millisecond)

	gate.release(readyAddr)
	if !waitReadyState(s, true, 3*time.Second) {
		t.Fatal("/readyz never became ready after the probe listener bound")
	}
}

// ---------------------------------------------------------------------------
// readyz gates on the main HTTP listener's bind
// ---------------------------------------------------------------------------

func TestReadyz_GatesOnMainListenerBind(t *testing.T) {
	cfg := config.Config{}
	cfg.Listeners.Ready.Enabled = true
	cfg.Listeners.Ready.Port = 19092
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 18080
	httpAddr := config.AddrFromPort(18080)

	// Only the main HTTP listener is held; the probe listener binds immediately.
	gate := installListenGate(t, httpAddr)
	s := NewServer(gateTestLogger(), cfg)
	runInBackground(t, s, gate)

	if !waitReadyState(s, false, 2*time.Second) {
		t.Fatal("/readyz never reported not-ready during the bind window")
	}
	// The probe listener binds at once; only the still-binding main listener keeps /readyz not-ready.
	assertStaysNotReady(t, s, 300*time.Millisecond)

	gate.release(httpAddr)
	if !waitReadyState(s, true, 3*time.Second) {
		t.Fatal("/readyz never became ready after the main HTTP listener bound")
	}
}

// ---------------------------------------------------------------------------
// readyz gates on an embedder listener's bind
// ---------------------------------------------------------------------------

func TestReadyz_GatesOnClientListenerBind(t *testing.T) {
	// No keel listener is enabled, so only the RegisterListener token can hold /readyz not-ready;
	// its onBound is deferred to the explicit call below.
	gate := installListenGate(t)
	s := NewServer(gateTestLogger(), config.Config{})
	onBound := s.RegisterListener("test-client")
	runInBackground(t, s, gate)

	if !waitReadyState(s, false, 2*time.Second) {
		t.Fatal("/readyz never reported not-ready before the client listener bound: RegisterListener is not enrolling in the readiness barrier")
	}
	assertStaysNotReady(t, s, 300*time.Millisecond)

	onBound() // client socket is now bound
	if !waitReadyState(s, true, 3*time.Second) {
		t.Fatal("/readyz never became ready after the client listener signalled onBound")
	}
}

// ---------------------------------------------------------------------------
// the bind path logs a failed bind at the bind site with its address and cause
// ---------------------------------------------------------------------------

func TestServeHTTPBound_LogsBindIntentAndFailure(t *testing.T) {
	orig := netListen
	netListen = func(_, _ string) (net.Listener, error) {
		return nil, errors.New("address already in use")
	}
	t.Cleanup(func() { netListen = orig })

	var buf bytes.Buffer
	log := logging.New(logging.Config{Level: "debug", Out: &buf})
	sd := lifecycle.NewShutdownOrchestrator(log)

	_ = serveHTTPBound(context.Background(), sd, "127.0.0.1:65535", http.NotFoundHandler(),
		config.Config{}, log, nil)

	out := buf.String()
	if !strings.Contains(out, "127.0.0.1:65535") {
		t.Fatalf("bind path did not log the listener address at debug level (intent); got: %q", out)
	}
	if !strings.Contains(out, "address already in use") {
		t.Fatalf("bind failure (\"could not bind port\") was not logged with its cause at the bind site; got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// backpressure still forces not-ready even when all listeners are serving
// ---------------------------------------------------------------------------

func TestReadyz_BackpressureStillShedsAfterListenersReady(t *testing.T) {
	s := newStubServer(t, config.Defaults())
	s.registerListenerReadinessCheck()
	s.listenersServing.Store(true)

	if !isReady(s) {
		t.Fatal("expected ready once listeners are serving and no backpressure")
	}
	s.readiness.Set(false)
	if isReady(s) {
		t.Fatal("backpressure Set(false) must force /readyz not-ready even when listeners are serving")
	}
}
