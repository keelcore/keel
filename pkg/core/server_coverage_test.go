// server_coverage_test.go — additional white-box unit tests raising package
// core statement coverage to 100%. These target error/success branches that
// are otherwise unreachable without crossing a system boundary; they use the
// package's test seams (netListen, acmeValidate, buildRemoteSinkFn,
// tracingSetupFn, acmeCertExpiryFn) and the injectable logger ExitFn.
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
	"github.com/keelcore/keel/pkg/core/acme"
	keelfips "github.com/keelcore/keel/pkg/core/fips"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/router"
	"github.com/keelcore/keel/pkg/core/tracing"
)

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
func (errAcceptListener) Close() error              { return nil }
func (errAcceptListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// ---------------------------------------------------------------------------
// serveHTTPBound — Serve returns a non-ErrServerClosed error (line: return err)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// serveHTTPS — net.Listen error and Serve error branches
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// realRunner wrappers — serveHTTP and serveHTTPS delegate to package funcs
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// startListenerReadinessGate — flips listenersServing once bindWG drains
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Metrics — accessor returns the server metrics store
// ---------------------------------------------------------------------------

func TestServer_Metrics_ReturnsStore(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	if s.Metrics() == nil {
		t.Fatal("Metrics() returned nil")
	}
}

// ---------------------------------------------------------------------------
// acmeLog — ACME manager error callback logs a warning
// ---------------------------------------------------------------------------

func TestAcmeLog_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	s := NewServer(logging.New(logging.Config{Out: &buf}), config.Config{})
	s.acmeLog("acme_test_event", map[string]any{"k": "v"})
	if !strings.Contains(buf.String(), "acme_test_event") {
		t.Errorf("expected acme_test_event in log, got %q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// applyRemoteSink — buildRemoteSink error branch
// ---------------------------------------------------------------------------

func TestApplyRemoteSink_BuildError(t *testing.T) {
	orig := buildRemoteSinkFn
	buildRemoteSinkFn = func(config.RemoteSinkConfig) (io.Writer, *logging.HTTPSink, error) {
		return nil, nil, errors.New("build boom")
	}
	t.Cleanup(func() { buildRemoteSinkFn = orig })

	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.runCtx = context.Background()
	cfg := config.Config{}
	cfg.Logging.RemoteSink.Enabled = true
	cfg.Logging.RemoteSink.Endpoint = "http://sink.invalid"
	s.applyRemoteSink(cfg) // must hit the Warn + Reconfigure error path
}

// ---------------------------------------------------------------------------
// applyTracing — tracing.Setup error branch
// ---------------------------------------------------------------------------

func TestApplyTracing_SetupError(t *testing.T) {
	orig := tracingSetupFn
	tracingSetupFn = func(config.OTLPConfig) (*tracing.Exporter, error) {
		return nil, errors.New("setup boom")
	}
	t.Cleanup(func() { tracingSetupFn = orig })

	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	cfg := config.Config{}
	cfg.Tracing.OTLP.Enabled = true
	cfg.Tracing.OTLP.Endpoint = "collector.invalid:4318"
	s.applyTracing(cfg) // must hit the Warn error path
}

// ---------------------------------------------------------------------------
// tickACMECertExpiry — success branch (CertExpiry returns no error)
// ---------------------------------------------------------------------------

func TestTickACMECertExpiry_Success(t *testing.T) {
	orig := acmeCertExpiryFn
	acmeCertExpiryFn = func(*acme.Manager) (float64, error) { return 4200, nil }
	t.Cleanup(func() { acmeCertExpiryFn = orig })

	tickACMECertExpiry(acme.New(), metrics.New()) // must call SetCertExpiry
}

// ---------------------------------------------------------------------------
// Fatal branches — driven via the panicking test logger
// ---------------------------------------------------------------------------

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

func TestStartSidecar_SignerError_Fatal(t *testing.T) {
	cfg := config.Config{}
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = "http://upstream.invalid"
	cfg.Authn.MyID = "me"
	cfg.Authn.MySignatureKeyFile = "/nonexistent/key.pem"
	s := NewServer(fatalLogger(), cfg)
	expectFatal(t, func() {
		s.startSidecar(context.Background(), router.New())
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

func TestRun_ACMEValidate_Fatal(t *testing.T) {
	orig := acmeValidate
	acmeValidate = func(config.Config) error { return errors.New("bad acme config") }
	t.Cleanup(func() { acmeValidate = orig })

	s := NewServer(fatalLogger(), config.Config{})
	expectFatal(t, func() { s.Run(context.Background()) })
}

func TestRun_FIPSMonitor_Fatal(t *testing.T) {
	if keelfips.Check() == nil {
		t.Skip("FIPS runtime active: fips_monitor_check would not fail")
	}
	cfg := config.Config{}
	cfg.FIPS.Monitor = true
	s := NewServer(fatalLogger(), cfg)
	expectFatal(t, func() { s.Run(context.Background()) })
}
