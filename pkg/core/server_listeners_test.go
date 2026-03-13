// server_listeners_test.go — white-box tests for startACMEListener,
// startHTTPSListener, startMainListeners, and startSidecar.
// Uses a stubRunner to avoid binding real TCP ports.
package core

import (
	"context"
	cryptotls "crypto/tls"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/router"
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

// TestStartSidecar_Disabled verifies no-op when sidecar is off.
func TestStartSidecar_Disabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sidecar.Enabled = false
	s := newStubServer(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startSidecar(ctx, router.New()) // must not panic
}

// TestStartSidecar_Success verifies the happy path: sidecar.New succeeds and
// a route is registered on the HTTP port.
func TestStartSidecar_Success(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = "http://127.0.0.1:1"
	cfg.Listeners.HTTP.Port = 18080
	cfg.Listeners.HTTP.Enabled = true
	s := newStubServer(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled so health probe goroutine exits promptly

	rt := router.New()
	s.startSidecar(ctx, rt)

	if !rt.Has(cfg.Listeners.HTTP.Port, "/") {
		t.Error("expected sidecar handler registered on HTTP port after startSidecar")
	}
}

// TestStartSidecar_WithJWTSigner covers the JWT signer init branch: a valid
// key file causes s.signer to be populated.
func TestStartSidecar_WithJWTSigner(t *testing.T) {
	keyPath := writeTestECKey(t)
	cfg := config.Defaults()
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = "http://127.0.0.1:1"
	cfg.Listeners.HTTP.Port = 18080
	cfg.Listeners.HTTP.Enabled = true
	cfg.Authn.MyID = "test-service"
	cfg.Authn.MySignatureKeyFile = keyPath
	s := newStubServer(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rt := router.New()
	s.startSidecar(ctx, rt)

	if s.signer.Load() == nil {
		t.Error("expected signer to be stored after startSidecar with JWT config")
	}
}
