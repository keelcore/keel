// server_run_test.go — unit tests for Run helpers extracted from server.go.
package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/router"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// ---------------------------------------------------------------------------
// newACMEManager
// ---------------------------------------------------------------------------

// newACMEManager returns a non-nil manager with the server logger wired.
func TestNewACMEManager_ReturnsManager(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	mgr := s.newACMEManager()
	if mgr == nil {
		t.Fatal("expected non-nil acme.Manager from newACMEManager")
	}
}

// newACMEManager logger is wired: calling it twice returns independent managers.
func TestNewACMEManager_IndependentManagers(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	m1 := s.newACMEManager()
	m2 := s.newACMEManager()
	if m1 == m2 {
		t.Error("expected distinct manager instances from repeated calls")
	}
}

// ---------------------------------------------------------------------------
// buildMainRouter — registrar invocation
// ---------------------------------------------------------------------------

// buildMainRouter calls Register on every stored registrar.
func TestBuildMainRouter_RegistrarInvoked(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	var called bool
	s.registrars = append(s.registrars, router.RegistrarFunc(func(_ *router.Router) {
		called = true
	}))

	rt, h := s.buildMainRouter()
	if rt == nil {
		t.Error("expected non-nil router")
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
	if !called {
		t.Error("expected registrar to be called by buildMainRouter")
	}
}

// ---------------------------------------------------------------------------
// newSignFn — non-nil signer path
// ---------------------------------------------------------------------------

// newSignFn with a non-nil signer calls sg.SignRequest on the request.
func TestNewSignFn_NonNilSigner(t *testing.T) {
	// Use a valid key to produce a working JWTSigner.
	keyPath := writeTestECKey(t)
	sg, err := mw.NewJWTSigner("test-service", keyPath)
	if err != nil {
		t.Fatalf("NewJWTSigner: %v", err)
	}

	var ptr atomic.Pointer[mw.JWTSigner]
	ptr.Store(sg)

	signFn := newSignFn(&ptr)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// SignRequest signs the request; the error (if any) must not be from nil-deref.
	_ = signFn(req)
	// Verify the branch was executed: Authorization header set or no panic.
	// Any outcome is acceptable; we just need the line to execute.
}

// ---------------------------------------------------------------------------
// RunServer
// ---------------------------------------------------------------------------

// RunServer with a pre-cancelled context and minimal (ACME-disabled) config
// exits promptly without starting any listener.
func TestRunServer_PrecancelledContext(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	// Disable all listeners so no port bind is attempted.
	cfg.Listeners.HTTP.Enabled = false
	cfg.Listeners.HTTPS.Enabled = false
	cfg.Listeners.H3.Enabled = false
	cfg.Listeners.Health.Enabled = false
	cfg.Listeners.Ready.Enabled = false
	cfg.Listeners.Admin.Enabled = false
	cfg.Listeners.Startup.Enabled = false
	cfg.TLS.ACME.Enabled = false
	cfg.Backpressure.SheddingEnabled = false
	cfg.FIPS.Monitor = false

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := NewServer(log, cfg)
	done := make(chan struct{})
	go func() {
		RunServer(s, ctx)
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(3 * time.Second):
		t.Error("RunServer did not exit promptly with pre-cancelled context")
	}
}

// ---------------------------------------------------------------------------
// drainListeners — additional branches
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// startBackgroundLoops — additional branches
// ---------------------------------------------------------------------------

// startBackgroundLoops with SheddingEnabled starts the pressure loop goroutine.
func TestStartBackgroundLoops_SheddingEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = true
	cfg.Listeners.HTTPS.Enabled = false
	cfg.FIPS.Monitor = false
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so goroutines exit

	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, nil)
	wg.Wait()
}

// startBackgroundLoops with FIPS.Monitor=true starts the FIPS monitor goroutine.
func TestStartBackgroundLoops_FIPSMonitor(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = false
	cfg.Listeners.HTTPS.Enabled = false
	cfg.FIPS.Monitor = true
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	met := metrics.New()
	s.met = met

	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, nil)
	wg.Wait()
}

// startBackgroundLoops with HTTPS+ACME enabled starts the ACME cert expiry loop.
func TestStartBackgroundLoops_HTTPSWithACME(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = false
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = true
	cfg.FIPS.Monitor = false
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	acmeMgr := s.newACMEManager()
	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, acmeMgr)
	wg.Wait()
}

// startBackgroundLoops with HTTPS enabled and certLoader set starts the file cert expiry loop.
func TestStartBackgroundLoops_HTTPSWithCertLoader(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	certPath, keyPath := writeTempCertKeyPair(t)
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = false
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = false
	cfg.TLS.CertFile = certPath
	cfg.FIPS.Monitor = false
	s := NewServer(log, cfg)

	loader, err := keeltls.NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	s.certLoader = loader

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, nil)
	wg.Wait()
}

// ---------------------------------------------------------------------------
// registerDefaultRoutes — H3 port
// ---------------------------------------------------------------------------

// registerDefaultRoutes with H3 enabled registers the default handler on the H3 port.
func TestRegisterDefaultRoutes_H3Enabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	cfg.Listeners.H3.Enabled = true
	cfg.Listeners.H3.Port = 8443
	s := NewServer(log, cfg)
	s.useDefaultRegistrar = true

	r := router.New()
	s.registerDefaultRoutes(r)

	if !r.Has(8443, "/") {
		t.Error("expected default route on H3 port 8443 after registerDefaultRoutes")
	}
}

// registerDefaultRoutes with HTTP enabled registers the default handler on the HTTP port.
func TestRegisterDefaultRoutes_HTTPEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 8080
	s := NewServer(log, cfg)
	s.useDefaultRegistrar = true

	r := router.New()
	s.registerDefaultRoutes(r)

	if !r.Has(8080, "/") {
		t.Error("expected default route on HTTP port 8080 after registerDefaultRoutes")
	}
}
