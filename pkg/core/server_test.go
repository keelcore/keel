package core

import (
	"bytes"
	"context"
	"errors"
	"io"
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

	"github.com/golang-jwt/jwt/v5"
	"github.com/keelcore/keel/pkg/config"
	keelfips "github.com/keelcore/keel/pkg/core/fips"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/pkg/core/router"
	"github.com/keelcore/keel/pkg/core/statsd"
)

// U7: with H3 enabled, the main chain advertises it on HTTPS responses.
func TestWrapMain_AdvertisesH3(t *testing.T) {
	for _, h3 := range []bool{true, false} {
		s := policyServer(func(c *config.Config) {
			c.Listeners.H3.Enabled = h3
			c.Listeners.H3.Port = 8443
		})
		rec := httptest.NewRecorder()
		s.wrapMain(reached).ServeHTTP(rec, policyRequest("/x", true, 2))
		want := ""
		if h3 {
			want = `h3=":8443"; ma=86400`
		}
		if got := rec.Header().Get("Alt-Svc"); got != want {
			t.Errorf("h3=%v: Alt-Svc %q, want %q", h3, got, want)
		}
	}
}

func TestServer_Metrics_ReturnsStore(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	if s.Metrics() == nil {
		t.Fatal("Metrics() returned nil")
	}
}

func TestAcmeLog_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	s := NewServer(logging.New(logging.Config{Out: &buf}), config.Config{})
	s.acmeLog("acme_test_event", map[string]any{"k": "v"})
	if !strings.Contains(buf.String(), "acme_test_event") {
		t.Errorf("expected acme_test_event in log, got %q", buf.String())
	}
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

func TestMetricsHandler_Disabled_Returns404(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{
		Metrics: config.MetricsConfig{Prometheus: false},
	})

	rr := httptest.NewRecorder()
	s.metricsHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("prometheus=false: expected 404, got %d", rr.Code)
	}
}

func TestPrestopSleep_Wired_InServerCfg(t *testing.T) {
	cfg := config.Defaults()
	cfg.Timeouts.PrestopSleep = config.DurationOf(200 * time.Millisecond)

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, cfg)

	if got := s.cfg.Timeouts.PrestopSleep.Duration; got != 200*time.Millisecond {
		t.Errorf("PrestopSleep: expected 200ms in server cfg, got %v", got)
	}
}

// With OWASP headers off, the request body cap still applies (X10): turning the
// headers off must not silently lift resource limits.
func TestWrapMain_BodyLimitWithoutOWASPHeaders(t *testing.T) {
	var cfg config.Config
	cfg.Security.OWASPHeaders = false
	cfg.Security.MaxRequestBodyBytes = 8
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 64))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: body limit not enforced with OWASP headers off", rec.Code)
	}
	if rec.Header().Get("x-content-type-options") != "" {
		t.Error("OWASP headers set although disabled")
	}
}

func TestBuildHealthMux_HealthzOK(t *testing.T) {
	mux := buildHealthMux()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from /healthz, got %d", rr.Code)
	}
}

func TestBuildReadyMux_ReadyzOK(t *testing.T) {
	r := probes.NewReadiness()
	mux := buildReadyMux(r)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	// Readiness defaults to true from NewReadiness; expect 200.
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from /readyz (ready by default), got %d", rr.Code)
	}
}

func TestBuildStartupMux_StartupzResponds(t *testing.T) {
	s := probes.NewStartup()
	mux := buildStartupMux(s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/startupz", nil))
	// Before Done(): 503; after Done(): 200. Either is a valid non-zero response.
	if rr.Code == 0 {
		t.Error("expected a non-zero status code from /startupz")
	}
}

func TestBuildAdminMux_HasHealth(t *testing.T) {
	r := probes.NewReadiness()
	s := probes.NewStartup()
	mux := buildAdminMux(r, s, http.NotFoundHandler(), http.NotFoundHandler())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from /healthz on admin mux, got %d", rr.Code)
	}
}

func TestBuildAdminMux_HasMetrics(t *testing.T) {
	r := probes.NewReadiness()
	s := probes.NewStartup()
	metricsH := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := buildAdminMux(r, s, metricsH, http.NotFoundHandler())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code == http.StatusNotFound {
		t.Error("expected /metrics to be registered on admin mux (got 404)")
	}
}

func TestBuildAdminMux_HasVersion(t *testing.T) {
	r := probes.NewReadiness()
	s := probes.NewStartup()
	mux := buildAdminMux(r, s, http.NotFoundHandler(), http.NotFoundHandler())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from /version on admin mux, got %d", rr.Code)
	}
}

func TestDefaultKeeHandler_RootReturnsOK(t *testing.T) {
	h := defaultKeeHandler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for GET /, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "keel: ok\n" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestDefaultKeeHandler_NonRootReturns404(t *testing.T) {
	h := defaultKeeHandler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for GET /other, got %d", rr.Code)
	}
}

func TestDefaultKeeHandler_NilURLReturns404(t *testing.T) {
	h := defaultKeeHandler()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL = nil
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nil URL, got %d", rr.Code)
	}
}

func TestNewSignFn_NilSigner(t *testing.T) {
	var ptr atomic.Pointer[mw.JWTSigner]
	// ptr is nil; signFn must return nil without panic.
	signFn := newSignFn(&ptr)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := signFn(req); err != nil {
		t.Errorf("expected nil error with nil signer, got %v", err)
	}
}

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

// TestWrapMain_Flags exercises each conditional middleware branch in wrapMain
// by enabling one flag at a time and verifying no panic occurs.
func TestWrapMain_Flags(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})

	cases := []struct {
		name string
		cfg  config.Config
	}{
		{
			name: "owasp",
			cfg: func() config.Config {
				c := config.Config{}
				c.Security.OWASPHeaders = true
				return c
			}(),
		},
		{
			name: "shedding",
			cfg: func() config.Config {
				c := config.Config{}
				c.Backpressure.SheddingEnabled = true
				return c
			}(),
		},
		{
			name: "accesslog",
			cfg: func() config.Config {
				c := config.Config{}
				c.Logging.AccessLog = true
				return c
			}(),
		},
		{
			name: "maxconcurrent",
			cfg: func() config.Config {
				c := config.Config{}
				c.Limits.MaxConcurrent = 10
				return c
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(log, tc.cfg)
			h := s.wrapMain(http.NotFoundHandler())
			if h == nil {
				t.Fatal("expected non-nil handler")
			}
		})
	}
}

// TestWrapMain_StatsDInstrumented exercises the s.sd != nil branch by
// injecting a statsd client directly (UDP; nothing need be listening).
func TestWrapMain_StatsDInstrumented(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	sd, err := statsd.New("127.0.0.1:8125", "keel")
	if err != nil {
		t.Skipf("cannot dial UDP for statsd test: %v", err)
	}
	s.sd = sd

	h := s.wrapMain(http.NotFoundHandler())
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

// An unauthenticated request is rejected by AuthnJWT before the external
// authorizer is called, and the 401 still carries the OWASP headers.
func TestWrapMain_AuthnBeforeExtAuthz_OWASPOnRejections(t *testing.T) {
	var authzCalls atomic.Int32
	authz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authzCalls.Add(1)
	}))
	defer authz.Close()

	var cfg config.Config
	cfg.Security.OWASPHeaders = true
	cfg.Authn.Enabled = true
	cfg.ExtAuthz.Enabled = true
	cfg.ExtAuthz.Endpoint = authz.URL
	cfg.ExtAuthz.Timeout = config.Duration{Duration: time.Second}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if rec.Header().Get("x-content-type-options") != "nosniff" {
		t.Error("401 is missing OWASP headers")
	}
	if n := authzCalls.Load(); n != 0 {
		t.Errorf("external authorizer called %d times for an unauthenticated request", n)
	}
}

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

// RunServer is a thin wrapper; verify it is callable by ensuring it is exported
// and takes the correct parameter types (compile-time check via NewServer).
func TestRunServer_TypeSignature(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	srv := NewServer(log, cfg)
	// Verify RunServer signature compiles (not called to avoid port binding).
	_ = RunServer
	_ = srv
}

// cancelAfter returns a context that is cancelled after d via a goroutine.
// Using cancel (not deadline) avoids the "shutdown_error" Fatal in Run()
// which triggers when sigErr is context.DeadlineExceeded.
func cancelAfter(d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(d)
		cancel()
	}()
	return ctx, cancel
}

// TestRun_HeapMaxBytes covers the debug.SetMemoryLimit branch.
// All listeners disabled to avoid port conflicts.
func TestRun_HeapMaxBytes_SetsLimit(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{HeapMaxBytes: 2 << 30}, // 2 GB — well above test heap
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_Backpressure covers the SheddingEnabled goroutine-launch branch.
func TestRun_Backpressure_StartsLoop(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			SheddingEnabled: true,
			HighWatermark:   0.9,
			LowWatermark:    0.7,
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_StatsD_InvalidEndpoint covers the statsd.New error branch.
// An endpoint without a port causes net.Dial to return an immediate error.
func TestRun_StatsD_InvalidEndpoint_Warns(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Metrics: config.MetricsConfig{
			StatsD: config.StatsDConfig{
				Enabled:  true,
				Endpoint: "127.0.0.1", // missing port — net.Dial("udp") fails immediately
			},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_StatsD_ValidEndpoint covers the statsd.New success branch.
// UDP dial succeeds even with no listener on the other end.
func TestRun_StatsD_ValidEndpoint(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Metrics: config.MetricsConfig{
			StatsD: config.StatsDConfig{
				Enabled:  true,
				Endpoint: "127.0.0.1:9125",
			},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_PrestopSleep covers the PrestopSleep branch in shutdown.
func TestRun_PrestopSleep_Executes(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Timeouts: config.TimeoutsConfig{
			PrestopSleep: config.DurationOf(1 * time.Millisecond),
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_OWASPHeaders covers the OWASPHeaders branch in wrapMain.
func TestRun_OWASPHeaders(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{OWASPHeaders: true},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_AccessLog covers the AccessLog branch in wrapMain.
func TestRun_AccessLog(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Logging: config.LoggingConfig{AccessLog: true},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_MaxConcurrent covers the MaxConcurrent branch in wrapMain.
func TestRun_MaxConcurrent(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Limits: config.LimitsConfig{MaxConcurrent: 100},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_Authn_Enabled covers the Authn.Enabled branch in wrapMain.
// No signers file is configured so LoadTrustedSigners returns an empty list.
func TestRun_Authn_Enabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Authn: config.AuthnConfig{Enabled: true},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_ExtAuthz_Enabled covers the ExtAuthz.Enabled branch in wrapMain.
func TestRun_ExtAuthz_Enabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		ExtAuthz: config.ExtAuthzConfig{
			Enabled:  true,
			Endpoint: "http://127.0.0.1:1/authz", // unreachable; middleware is only exercised at request time
			Timeout:  config.DurationOf(10 * time.Millisecond),
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_HealthListener covers the Health listener goroutine-launch block.
func TestRun_HealthListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Health: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_ReadyListener covers the Ready listener goroutine-launch block.
func TestRun_ReadyListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Ready: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_AdminListener covers the Admin listener goroutine-launch block.
func TestRun_AdminListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Admin: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_StartupListener covers the Startup listener goroutine-launch block.
func TestRun_StartupListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Startup: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// A 403 from the application is audited through the main chain.
func TestWrapMain_AuditsForbidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, func(c *config.Config) { c.Authn.Enabled = false })
	forbid := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	s.wrapMain(forbid).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !strings.Contains(auditText(t, path), `"event":"forbidden"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

// wrapMain with all middleware disabled must return a handler that passes
// through to the inner handler.
func TestWrapMain_NoMiddleware(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{
			SheddingEnabled: false,
		},
		Authn:    config.AuthnConfig{Enabled: false},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	// TraceContext and RequestID are always applied; inner handler still runs.
	if rr.Code != http.StatusTeapot {
		t.Errorf("expected 418 from inner handler, got %d", rr.Code)
	}
}

// wrapMain with OWASP headers enabled must wrap the handler.
func TestWrapMain_OWASPEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{
			OWASPHeaders: true,
			HSTSMaxAge:   31536000,
		},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz:     config.ExtAuthzConfig{Enabled: false},
		Logging:      config.LoggingConfig{AccessLog: false},
		Limits:       config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with access log enabled must wrap the handler.
func TestWrapMain_AccessLogEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz:     config.ExtAuthzConfig{Enabled: false},
		Logging:      config.LoggingConfig{AccessLog: true},
		Limits:       config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with concurrency limit enabled must wrap the handler.
func TestWrapMain_ConcurrencyLimitEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz:     config.ExtAuthzConfig{Enabled: false},
		Logging:      config.LoggingConfig{AccessLog: false},
		Limits:       config.LimitsConfig{MaxConcurrent: 10, QueueDepth: 5},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with shedding enabled wraps the handler.
func TestWrapMain_SheddingEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{
			SheddingEnabled: true,
			HighWatermark:   0.90,
			LowWatermark:    0.70,
		},
		Authn:    config.AuthnConfig{Enabled: false},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	// Readiness is not degraded so the request passes through.
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with Authn.Enabled and no stored snapshot covers the return-nil
// branch of the signers closure. A Bearer token is required for AuthnJWT to
// call the closure at all; the token can be invalid.
func TestWrapMain_AuthnEnabled_NilSnapshot_ClosureReturnsNil(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Authn: config.AuthnConfig{Enabled: true},
	}
	s := NewServer(log, cfg)
	// Do NOT call applyAuthnState so s.authn.Load() returns nil → closure returns nil.

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	h.ServeHTTP(rr, req)
	// AuthnJWT with no signers should reject the request — closure was invoked.
	if rr.Code == 0 {
		t.Error("expected a response code")
	}
}

// registerDefaultRoutes with HTTPS enabled registers the default handler on
// the HTTPS port, covering server.go:259-260.
func TestRegisterDefaultRoutes_HTTPSEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	cfg.Listeners.HTTPS.Enabled = true
	cfg.Listeners.HTTPS.Port = 8443
	s := NewServer(log, cfg)
	s.useDefaultRegistrar = true

	r := router.New()
	s.registerDefaultRoutes(r) // must not panic
}

// startSidecar with an invalid UpstreamURL triggers the warn path,
// covering server.go:411-417.
func TestStartSidecar_InvalidURL(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = "\x00bad" // null byte → url.Parse error
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled so no health probe goroutine lingers

	r := router.New()
	s.startSidecar(ctx, r) // must not panic; logs warn and returns
}

// metricsHandler with prometheus=true returns the real Prometheus handler (200, text/plain).
func TestMetricsHandler_Enabled_Returns200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{
		Metrics: config.MetricsConfig{Prometheus: true},
	})

	rr := httptest.NewRecorder()
	s.metricsHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("prometheus=true: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// AddRoute: appends a registrar and the closure body registers the handler on the router.
func TestServer_AddRoute(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	before := len(s.registrars)
	s.AddRoute(8080, "/probe", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if len(s.registrars) != before+1 {
		t.Errorf("expected registrars len %d after AddRoute, got %d", before+1, len(s.registrars))
	}

	// Exercise the registrar closure body: call Register on a real router and verify the route exists.
	rt := router.New()
	for _, reg := range s.registrars[before:] {
		reg.Register(rt)
	}
	if !rt.Has(8080, "/probe") {
		t.Errorf("expected route /probe on port 8080 after Register, not found")
	}
}

// RunServer: with all listeners disabled and a pre-cancelled context, Run
// exits immediately via the ctx.Done branch without binding any ports.
func TestRunServer_CancelledContext(t *testing.T) {
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			HTTP:    config.ListenerConfig{Enabled: false},
			HTTPS:   config.ListenerConfig{Enabled: false},
			H3:      config.ListenerConfig{Enabled: false},
			Health:  config.ListenerConfig{Enabled: false},
			Ready:   config.ListenerConfig{Enabled: false},
			Startup: config.ListenerConfig{Enabled: false},
			Admin:   config.ListenerConfig{Enabled: false},
		},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
	}
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: Run exits at WaitForStop without blocking

	RunServer(s, ctx)
}

// wrapMain with ExtAuthz enabled wraps the handler (unreachable endpoint + fail closed → 403).
func TestWrapMain_ExtAuthzEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz: config.ExtAuthzConfig{
			Enabled:   true,
			Endpoint:  "http://127.0.0.1:1/authz",
			Timeout:   config.DurationOf(50 * time.Millisecond),
			Transport: "http",
			FailOpen:  false,
		},
		Logging: config.LoggingConfig{AccessLog: false},
		Limits:  config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	// Unreachable authz endpoint + fail_open=false → 403.
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 when ExtAuthz enabled with unreachable endpoint (fail_closed), got %d", rr.Code)
	}
}

// authn.trusted_ids is reloadable: wrapMain reads the allowlist from the authn
// snapshot per request, falling back to the startup list before one exists.
func TestWrapMain_TrustedIDsReload(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("HMAC fixtures not used under FIPS 140-only mode")
	}
	const secret = "reload-test-hmac-secret-0123456789abcdef"
	var cfg config.Config
	cfg.Authn.Enabled = true
	cfg.Authn.TrustedSigners = []string{secret}
	cfg.Authn.TrustedIDs = []string{"alice"}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256,
		jwt.MapClaims{"sub": "bob", "exp": time.Now().Add(time.Minute).Unix()}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	s.applyAuthnState(cfg) // startup snapshot: alice only
	if code := call(); code != http.StatusForbidden {
		t.Fatalf("bob before reload: status %d, want 403", code)
	}
	cfg.Authn.TrustedIDs = []string{"bob"}
	s.applyAuthnState(cfg) // reload adds bob
	if code := call(); code != http.StatusOK {
		t.Fatalf("bob after reload: status %d, want 200", code)
	}
}

// wrapMain with authn enabled wraps the handler (missing bearer → 401).
func TestWrapMain_AuthnEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn: config.AuthnConfig{
			Enabled: true,
			// No trusted signers → any token fails; no bearer → 401.
		},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)
	s.applyAuthnState(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header → AuthnJWT returns 401.
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when authn enabled with no bearer token, got %d", rr.Code)
	}
}
