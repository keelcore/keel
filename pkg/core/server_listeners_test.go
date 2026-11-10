// server_listeners_test.go — white-box tests for startACMEListener,
// startHTTPSListener, startMainListeners, and startSidecar.
// Uses a stubRunner to avoid binding real TCP ports.
package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/protonotice"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// stubRunner satisfies listenerRunner without opening any ports.
type stubRunner struct{}

func (stubRunner) serveHTTP(_ context.Context, _ *lifecycle.Orchestrator, _ string,
	_ http.Handler, _ config.Config, _ *logging.Logger) error {
	return nil
}

func (stubRunner) serveHTTPS(_ context.Context, _ *lifecycle.Orchestrator, _ string,
	_ http.Handler, _ config.Config, _ *keeltls.CertLoader,
	_ func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error),
	_ *logging.Logger) error {
	return nil
}

func (stubRunner) serveH3(_ context.Context, _ string, _ http.Handler,
	_ config.Config, _ *logging.Logger) error {
	return nil
}

// newStubServer returns a Server with stubRunner injected.
func newStubServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, cfg)
	s.runner = stubRunner{}
	return s
}

// newStubSD returns a lifecycle.Orchestrator wired to the server logger.
func newStubSD(s *Server) *lifecycle.Orchestrator {
	return lifecycle.NewShutdownOrchestrator(s.logger)
}

// ---------------------------------------------------------------------------
// startACMEListener
// ---------------------------------------------------------------------------

// TestStartACMEListener_Disabled verifies nil is returned when ACME is off.
func TestStartACMEListener_Disabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.TLS.ACME.Enabled = false
	s := newStubServer(t, cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := s.startACMEListener(ctx, newStubSD(s), &wg, errCh)
	if mgr != nil {
		t.Error("expected nil manager when ACME is disabled")
	}
}

// TestStartACMEListener_Enabled verifies a non-nil manager is returned and
// the stub goroutine completes (wg.Wait returns).
func TestStartACMEListener_Enabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.test"}
	cfg.TLS.ACME.Email = "test@example.test"
	cfg.TLS.ACME.ChallengePort = 18080
	cfg.Listeners.HTTPS.Port = 18443
	s := newStubServer(t, cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := s.startACMEListener(ctx, newStubSD(s), &wg, errCh)
	if mgr == nil {
		t.Fatal("expected non-nil acme.Manager when ACME is enabled")
	}
	wg.Wait() // stubRunner returns nil immediately
}

// ---------------------------------------------------------------------------
// startHTTPSListener
// ---------------------------------------------------------------------------

// TestStartHTTPSListener_Disabled verifies no goroutine is added when HTTPS
// is disabled.
func TestStartHTTPSListener_Disabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTPS.Enabled = false
	s := newStubServer(t, cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startHTTPSListener(ctx, newStubSD(s), &wg, errCh, http.NotFoundHandler(), nil)
	// If a goroutine were added, wg.Wait() would not return, but we just check
	// no panic and move on.
}

// TestStartHTTPSListener_ACMEPath verifies the ACME branch runs without error.
func TestStartHTTPSListener_ACMEPath(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = true
	cfg.Listeners.HTTPS.Port = 18443
	s := newStubServer(t, cfg)

	acmeMgr := s.newACMEManager()

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startHTTPSListener(ctx, newStubSD(s), &wg, errCh, http.NotFoundHandler(), acmeMgr)
	wg.Wait() // stubRunner returns nil immediately
}

// TestStartHTTPSListener_FileCertPath verifies s.certLoader is populated and
// the goroutine completes on the file-cert branch.
func TestStartHTTPSListener_FileCertPath(t *testing.T) {
	certPath, keyPath := writeTempCertKeyPair(t)
	cfg := config.Defaults()
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = false
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath
	s := newStubServer(t, cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startHTTPSListener(ctx, newStubSD(s), &wg, errCh, http.NotFoundHandler(), nil)
	wg.Wait()

	if s.certLoader == nil {
		t.Error("expected certLoader to be set after startHTTPSListener (file-cert path)")
	}
}

// ---------------------------------------------------------------------------
// startMainListeners
// ---------------------------------------------------------------------------

// TestStartMainListeners_HTTP verifies the HTTP listener goroutine is started.
func TestStartMainListeners_HTTP(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 18080
	cfg.Listeners.HTTPS.Enabled = false
	cfg.Listeners.H3.Enabled = false
	s := newStubServer(t, cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startMainListeners(ctx, newStubSD(s), &wg, errCh, http.NotFoundHandler(), nil)
	wg.Wait()
}

// TestStartMainListeners_H3WithCert verifies the H3 listener goroutine is
// started when cert files are provided.
func TestStartMainListeners_H3WithCert(t *testing.T) {
	certPath, keyPath := writeTempCertKeyPair(t)
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = false
	cfg.Listeners.HTTPS.Enabled = false
	cfg.Listeners.H3.Enabled = true
	cfg.Listeners.H3.Port = 18443
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath
	s := newStubServer(t, cfg)

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startMainListeners(ctx, newStubSD(s), &wg, errCh, http.NotFoundHandler(), nil)
	wg.Wait()
}

// ---------------------------------------------------------------------------
// startSidecar
// ---------------------------------------------------------------------------

// syncBuffer is a bytes.Buffer safe for a logger goroutine and a test reader.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// U6: plain HTTP sent to the HTTPS port gets the KEEL-PROTO-006 rejection (301 to
// the notice), not net/http's built-in 400, and is counted.
func TestServeHTTPS_PlaintextGetsTheRejection(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	addrCh := make(chan string, 1)
	orig := netListen
	netListen = func(network, _ string) (net.Listener, error) {
		ln, err := orig(network, "127.0.0.1:0")
		if err == nil {
			addrCh <- ln.Addr().String()
		}
		return ln, err
	}
	t.Cleanup(func() { netListen = orig })

	var mu sync.Mutex
	var rejected []protonotice.Code
	r := realRunner{onRejected: func(c protonotice.Code) {
		mu.Lock()
		defer mu.Unlock()
		rejected = append(rejected, c)
	}}
	cfg := shortDrainCfg()
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 8080
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.serveHTTPS(ctx, lifecycle.NewShutdownOrchestrator(log), "127.0.0.1:0", http.NotFoundHandler(), cfg, loader, nil, log)
	}()
	addr := <-addrCh

	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("plain HTTP to the HTTPS port: %v", err)
	}
	_ = resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	host, _, _ := net.SplitHostPort(addr)
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Keel-Error-Code") != string(protonotice.PlaintextOnHTTPS) ||
		resp.Header.Get("Location") != "http://"+host+":8080/.well-known/keel/upgrade?code=KEEL-PROTO-006" {
		t.Fatalf("got %d %v", resp.StatusCode, resp.Header)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rejected) != 1 || rejected[0] != protonotice.PlaintextOnHTTPS {
		t.Fatalf("counted %v, want [KEEL-PROTO-006]", rejected)
	}
}

// U1 wiring: the HTTPS listener reports a refused handshake to the runner's observer.
func TestServeHTTPS_ReportsRefusedHandshakes(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	addrCh := make(chan string, 1)
	orig := netListen
	netListen = func(network, _ string) (net.Listener, error) {
		ln, err := orig(network, "127.0.0.1:0")
		if err == nil {
			addrCh <- ln.Addr().String()
		}
		return ln, err
	}
	t.Cleanup(func() { netListen = orig })

	var mu sync.Mutex
	var seen []protonotice.Code
	r := realRunner{onReject: func(c protonotice.Code, _ *cryptotls.ClientHelloInfo) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
	}}
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.serveHTTPS(ctx, lifecycle.NewShutdownOrchestrator(log), "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), loader, nil, log)
	}()
	addr := <-addrCh

	conn, err := cryptotls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr,
		&cryptotls.Config{MaxVersion: cryptotls.VersionTLS12, ServerName: "test"})
	if err == nil {
		_ = conn.Close()
		t.Fatal("TLS 1.2 handshake succeeded, want it refused")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != protonotice.TLSVersion {
		t.Fatalf("observed %v, want [%s]", seen, protonotice.TLSVersion)
	}
}

// errFatalSentinel is panicked by the test logger's ExitFn so a Fatal call
// unwinds to expectFatal instead of terminating the test process.
var errFatalSentinel = errors.New("fatal-sentinel")

// fatalLogger returns a logger whose Fatal panics (via ExitFn) so Fatal
// branches can be exercised and recovered in-process.
func fatalLogger() *logging.Logger {
	log := logging.New(logging.Config{Out: io.Discard})
	log.ExitFn = func(int) { panic(errFatalSentinel) }
	return log
}

// expectFatal asserts fn triggers exactly one logger.Fatal (our sentinel panic).
func expectFatal(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a Fatal (sentinel panic) but none occurred")
		}
		if r != errFatalSentinel {
			panic(r) // unexpected panic — re-raise
		}
	}()
	fn()
}

// errAcceptListener is a net.Listener whose Accept always fails, forcing
// http.Server.Serve to return a non-ErrServerClosed error without any real
// socket or peer.
type errAcceptListener struct{}

func (errAcceptListener) Accept() (net.Conn, error) { return nil, errors.New("accept boom") }

func (errAcceptListener) Close() error { return nil }

func (errAcceptListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func TestServeHTTPBound_ServeError(t *testing.T) {
	orig := netListen
	netListen = func(string, string) (net.Listener, error) { return errAcceptListener{}, nil }
	t.Cleanup(func() { netListen = orig })

	log := logging.New(logging.Config{Out: io.Discard})
	sd := lifecycle.NewShutdownOrchestrator(log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	err := serveHTTPBound(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), log, nil)
	if err == nil {
		t.Fatal("expected Serve error to propagate")
	}
}

// TestServeHTTPS_DefaultDrain exercises the drain==0 → default 10s branch in
// the shutdown goroutine: a real loopback listener with ShutdownDrain unset,
// cancelled cleanly (no live connections, so Shutdown returns at once).
func TestServeHTTPS_DefaultDrain(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	sd := lifecycle.NewShutdownOrchestrator(log)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		// config.Config{} → ShutdownDrain == 0 → the default-drain branch runs.
		errCh <- serveHTTPS(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), config.Config{}, nil, nil, log)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected nil on clean shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveHTTPS did not exit after cancel")
	}
}

func TestServeHTTPS_ServeError(t *testing.T) {
	orig := netListen
	netListen = func(string, string) (net.Listener, error) { return errAcceptListener{}, nil }
	t.Cleanup(func() { netListen = orig })

	log := logging.New(logging.Config{Out: io.Discard})
	sd := lifecycle.NewShutdownOrchestrator(log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	err := serveHTTPS(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), nil, nil, log)
	if err == nil {
		t.Fatal("expected Serve error to propagate")
	}
}

func TestRealRunner_ServeHTTP_And_ServeHTTPS(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	sd := lifecycle.NewShutdownOrchestrator(log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled: bind then immediate clean shutdown

	if err := (realRunner{}).serveHTTP(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), log); err != nil {
		t.Errorf("realRunner.serveHTTP: %v", err)
	}
	if err := (realRunner{}).serveHTTPS(ctx, sd, "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), nil, nil, log); err != nil {
		t.Errorf("realRunner.serveHTTPS: %v", err)
	}
}

func TestStartListenerReadinessGate_Flips(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.startListenerReadinessGate() // bindWG == 0 → gate flips immediately

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s.listenersServing.Load() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("readiness gate did not flip listenersServing")
}

func TestStartHTTPSListener_NoCert_Fatal(t *testing.T) {
	cfg := config.Config{}
	cfg.Listeners.HTTPS.Enabled = true
	s := NewServer(fatalLogger(), cfg)
	expectFatal(t, func() {
		s.startHTTPSListener(context.Background(), lifecycle.NewShutdownOrchestrator(s.logger),
			&sync.WaitGroup{}, make(chan error, 1), http.NotFoundHandler(), nil)
	})
}

func TestStartHTTPSListener_LoaderError_Fatal(t *testing.T) {
	cfg := config.Config{}
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.CertFile = "/nonexistent/cert.pem"
	cfg.TLS.KeyFile = "/nonexistent/key.pem"
	s := NewServer(fatalLogger(), cfg)
	expectFatal(t, func() {
		s.startHTTPSListener(context.Background(), lifecycle.NewShutdownOrchestrator(s.logger),
			&sync.WaitGroup{}, make(chan error, 1), http.NotFoundHandler(), nil)
	})
}

func TestStartMainListeners_H3NoCert_Fatal(t *testing.T) {
	cfg := config.Config{}
	cfg.Listeners.H3.Enabled = true // HTTP + HTTPS disabled; H3 with no cert → Fatal
	s := NewServer(fatalLogger(), cfg)
	expectFatal(t, func() {
		s.startMainListeners(context.Background(), lifecycle.NewShutdownOrchestrator(s.logger),
			&sync.WaitGroup{}, make(chan error, 1), http.NotFoundHandler(), nil)
	})
}

func TestDrainListeners_ListenerError_Fatal(t *testing.T) {
	s := NewServer(fatalLogger(), config.Config{})
	errCh := make(chan error, 1)
	errCh <- errors.New("listener boom")
	expectFatal(t, func() {
		s.drainListeners(func() {}, &sync.WaitGroup{}, errCh, nil)
	})
}

func TestDrainListeners_ShutdownError_Fatal(t *testing.T) {
	s := NewServer(fatalLogger(), config.Config{})
	errCh := make(chan error) // empty → default branch
	expectFatal(t, func() {
		s.drainListeners(func() {}, &sync.WaitGroup{}, errCh, errors.New("shutdown boom"))
	})
}

func TestPrestopSleep_Default_IsZero(t *testing.T) {
	cfg := config.Defaults()
	if d := cfg.Timeouts.PrestopSleep.Duration; d != 0 {
		t.Errorf("default PrestopSleep should be 0, got %v", d)
	}
}

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

func TestNewHTTPServer_FieldsSet(t *testing.T) {
	cfg := config.Config{
		Security: config.SecurityConfig{MaxHeaderBytes: 4096},
		Timeouts: config.TimeoutsConfig{
			ReadHeader: config.DurationOf(5 * time.Second),
			Read:       config.DurationOf(10 * time.Second),
			Write:      config.DurationOf(15 * time.Second),
			Idle:       config.DurationOf(60 * time.Second),
		},
	}
	h := http.NotFoundHandler()
	srv := newHTTPServer(":9999", h, cfg)
	if srv.Addr != ":9999" {
		t.Errorf("Addr: expected :9999, got %s", srv.Addr)
	}
	if srv.MaxHeaderBytes != 4096 {
		t.Errorf("MaxHeaderBytes: expected 4096, got %d", srv.MaxHeaderBytes)
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout: expected 5s, got %v", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout: expected 10s, got %v", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 15*time.Second {
		t.Errorf("WriteTimeout: expected 15s, got %v", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout: expected 60s, got %v", srv.IdleTimeout)
	}
}

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

// generateTestCert writes a self-signed ECDSA cert and key to temp files and
// returns their paths.  Files live inside t.TempDir() and are removed when the
// test ends.
func generateTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(cryptorand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	certF, err := os.CreateTemp(dir, "cert*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(certF, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatal(err)
	}
	certF.Close()

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyF, err := os.CreateTemp(dir, "key*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(keyF, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	keyF.Close()

	return certF.Name(), keyF.Name()
}

// shortDrainCfg returns a Config with a 1 ms shutdown drain so tests that
// pre-cancel the context complete in microseconds instead of the default 10 s.
func shortDrainCfg() config.Config {
	cfg := config.Config{}
	cfg.Timeouts.ShutdownDrain = config.DurationOf(time.Millisecond)
	return cfg
}

// TestServeHTTPS_CancelledContext: the TLS listener binds on a random port,
// the context is pre-cancelled, serveHTTPS shuts down cleanly and returns nil.
func TestServeHTTPS_CancelledContext(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := serveHTTPS(ctx, shutdown, "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), loader, nil, log); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestServeHTTPS_ListenError: an invalid address causes net.Listen to fail;
// serveHTTPS returns an error without blocking.
func TestServeHTTPS_ListenError(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	err = serveHTTPS(context.Background(), shutdown, "256.0.0.1:0",
		http.NotFoundHandler(), config.Config{}, loader, nil, log)
	if err == nil {
		t.Fatal("expected error from invalid address, got nil")
	}
}

// TestServeH3_CertMissing: ListenAndServeTLS fails immediately when cert files
// do not exist; serveH3 returns the error via the errCh select branch.
func TestServeH3_CertMissing(t *testing.T) {
	cfg := config.Config{}
	cfg.TLS.CertFile = "/nonexistent-cert-for-test.pem"
	cfg.TLS.KeyFile = "/nonexistent-key-for-test.pem"

	err := serveH3(context.Background(), "127.0.0.1:18443",
		http.NotFoundHandler(), cfg, logging.New(logging.Config{Out: io.Discard}))
	if err == nil {
		t.Fatal("expected error from missing cert files, got nil")
	}
}

// With tls.client_ca_file set, the handshake requires a client certificate
// chaining to that CA; without the file, client certs are not requested.
func TestApplyClientCA_RequiresVerifiedClientCert(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" || strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
		t.Skip("self-signed certs not permitted under FIPS 140-only mode")
	}
	var none cryptotls.Config
	if err := applyClientCA(&none, ""); err != nil || none.ClientAuth != cryptotls.NoClientCert {
		t.Fatalf("unset file: err=%v auth=%v", err, none.ClientAuth)
	}

	caFile, clientPair := newClientCA(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &cryptotls.Config{MinVersion: cryptotls.VersionTLS13}
	if err := applyClientCA(srv.TLS, caFile); err != nil {
		t.Fatal(err)
	}
	srv.StartTLS()
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	get := func(certs []cryptotls.Certificate) error {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: &cryptotls.Config{RootCAs: roots, Certificates: certs}}}
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get(nil); err == nil {
		t.Fatal("request without a client certificate succeeded")
	}
	if err := get([]cryptotls.Certificate{clientPair}); err != nil {
		t.Fatalf("request with a trusted client certificate failed: %v", err)
	}
}

// An unreadable client CA fails the HTTPS listener before it binds.
func TestServeHTTPSBound_BadClientCAFails(t *testing.T) {
	var cfg config.Config
	cfg.TLS.ClientCAFile = filepath.Join(t.TempDir(), "missing-ca.pem")
	log := logging.New(logging.Config{Out: io.Discard})
	fired := false
	err := serveHTTPSBound(context.Background(), lifecycle.NewShutdownOrchestrator(log), "127.0.0.1:0",
		http.NotFoundHandler(), cfg, nil, nil, log, func() { fired = true })
	if err == nil || !strings.Contains(err.Error(), "client_ca_file") {
		t.Fatalf("err = %v, want client_ca_file error", err)
	}
	if fired {
		t.Fatal("listener reported bound despite the bad client CA")
	}
}

// newClientCA writes a CA certificate to a temp file and returns it with a
// client certificate (ExtKeyUsageClientAuth) that the CA signed.
func newClientCA(t *testing.T) (string, cryptotls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test client CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(cryptorand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "client"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(cryptorand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "client-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile, cryptotls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
}

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

func isReady(s *Server) bool {
	ok, _ := s.readiness.IsReady()
	return ok
}

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

// drainListeners with sigErr=context.Canceled covers the `if sigErr != nil`
// cancel branch and verifies no fatal is called (context.Canceled is expected).
func TestDrainListeners_SigErrCanceled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	var cancelCalled bool
	cancelFn := func() { cancelCalled = true }

	var wg sync.WaitGroup
	errCh := make(chan error, 4) // empty

	s.drainListeners(cancelFn, &wg, errCh, context.Canceled)

	if !cancelCalled {
		t.Error("expected cancel() to be called when sigErr is non-nil")
	}
}

// drainListeners with a nil error on errCh covers the `case err := <-errCh:` branch.
func TestDrainListeners_ErrChNilError(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	var cancelCalled bool
	cancelFn := func() { cancelCalled = true }

	errCh := make(chan error, 1)
	errCh <- nil // nil error: no fatal

	var wg sync.WaitGroup
	s.drainListeners(cancelFn, &wg, errCh, nil)

	if !cancelCalled {
		t.Error("expected cancel() to be called in errCh case branch")
	}
}

// serveHTTP returns an error when the address is already in use.
func TestServeHTTP_BindError(t *testing.T) {
	// Bind a port first so the second bind fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(ctx, shutdown, addr, http.NotFoundHandler(), cfg, log)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected non-nil error when address is already in use")
		}
	case <-time.After(2 * time.Second):
		t.Error("serveHTTP did not return within timeout")
	}
}

// serveHTTP returns nil when the context is cancelled (clean shutdown).
func TestServeHTTP_CleanShutdown(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Timeouts: config.TimeoutsConfig{
			ShutdownDrain: config.DurationOf(100 * time.Millisecond),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(ctx, shutdown, "127.0.0.1:0", h, cfg, log)
	}()

	// Give serveHTTP a moment to bind, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected nil error on clean shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("serveHTTP did not exit within timeout after ctx cancel")
	}
}

// serveHTTPS returns an error when the address is already in use.
func TestServeHTTPS_BindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPS(ctx, shutdown, addr, http.NotFoundHandler(), cfg, nil, nil, log)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected non-nil error when address is already in use")
		}
	case <-time.After(2 * time.Second):
		t.Error("serveHTTPS did not return within timeout")
	}
}
