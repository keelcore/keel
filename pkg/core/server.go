// pkg/core/server.go
package core

import (
	"context"
	cryptotls "crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
	keelfips "github.com/keelcore/keel/pkg/core/fips"
	"github.com/keelcore/keel/pkg/core/http3"
	"github.com/keelcore/keel/pkg/core/httpx"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/pkg/core/router"
	"github.com/keelcore/keel/pkg/core/sidecar"
	"github.com/keelcore/keel/pkg/core/statsd"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
	"github.com/keelcore/keel/pkg/core/tracing"
	"github.com/keelcore/keel/pkg/core/version"
)

// Test seams: package-level indirections defaulting to the real library or
// package functions so production behaviour is unchanged. Tests override these
// to exercise error/success branches that are otherwise unreachable without
// crossing a system boundary (a real listen, a real ACME cert, a live sink).
var (
	netListen         = net.Listen
	acmeValidate      = acme.Validate
	buildRemoteSinkFn = buildRemoteSink
	tracingSetupFn    = tracing.Setup
	acmeCertExpiryFn  = (*acme.Manager).CertExpiry
)

// authnSnapshot holds the precomputed authn state used by AuthnJWT middleware.
// It is rebuilt on every SIGHUP reload via applyAuthnState.
type authnSnapshot struct {
	signers []string
}

// listenerRunner abstracts the three serve* functions so tests can stub them
// without binding real TCP ports.
type listenerRunner interface {
	serveHTTP(ctx context.Context, sd *lifecycle.Orchestrator, addr string,
		h http.Handler, cfg config.Config, log *logging.Logger) error
	serveHTTPS(ctx context.Context, sd *lifecycle.Orchestrator, addr string,
		h http.Handler, cfg config.Config, loader *keeltls.CertLoader,
		getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error),
		log *logging.Logger) error
	serveH3(ctx context.Context, addr string, h http.Handler,
		cfg config.Config, log *logging.Logger) error
}

// realRunner is the production implementation of listenerRunner. h3Cert is the
// shared certificate source for HTTP/3 (nil: serveH3 loads the cfg cert files).
type realRunner struct {
	onBound func()
	h3Cert  func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error)
}

func (r realRunner) serveHTTP(ctx context.Context, sd *lifecycle.Orchestrator, addr string,
	h http.Handler, cfg config.Config, log *logging.Logger) error {
	return serveHTTPBound(ctx, sd, addr, h, cfg, log, r.onBound)
}

func (r realRunner) serveHTTPS(ctx context.Context, sd *lifecycle.Orchestrator, addr string,
	h http.Handler, cfg config.Config, loader *keeltls.CertLoader,
	getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error),
	log *logging.Logger) error {
	return serveHTTPSBound(ctx, sd, addr, h, cfg, loader, getCert, log, r.onBound)
}

func (r realRunner) serveH3(ctx context.Context, addr string, h http.Handler,
	cfg config.Config, log *logging.Logger) error {
	return serveH3With(ctx, addr, h, cfg, log, r.onBound, r.h3Cert)
}

type Server struct {
	cfg      config.Config
	cfgMu    sync.RWMutex
	cfgPaths [2]string // [configPath, secretsPath] for Reload

	registrars          []router.Registrar
	useDefaultRegistrar bool
	readiness           *probes.Readiness
	startup             *probes.Startup
	logger              *logging.Logger
	met                 *metrics.Metrics
	sd                  *statsd.Client
	certLoader          *keeltls.CertLoader
	runner              listenerRunner

	// Remote sink lifecycle. runCtx is set once in Run and used by
	// applyRemoteSink to derive sink goroutine contexts so they are
	// cancelled on both SIGHUP reload and process shutdown.
	// sinkWg tracks the active sink goroutine so Run waits for its final
	// flush to complete before the process exits.
	// sinkOut is the writer currently attached to the logger (nil: none); it is
	// detached and, if it is an io.Closer (syslog), closed when replaced.
	runCtx     context.Context
	sinkMu     sync.Mutex
	sinkCancel context.CancelFunc
	sinkWg     sync.WaitGroup
	httpSink   atomic.Pointer[logging.HTTPSink]
	sinkOut    io.Writer

	// Outbound signer: updated atomically on SIGHUP so the sidecar proxy
	// picks up the new key without being re-created.
	signer atomic.Pointer[mw.JWTSigner]
	authn  atomic.Pointer[authnSnapshot]

	// OTLP tracing: tracingMu serialises Setup/Shutdown across concurrent
	// SIGHUP reloads; expPtr is read per-request via the OTelSpan middleware.
	tracingMu sync.Mutex
	expPtr    atomic.Pointer[tracing.Exporter]

	// Listener-readiness bind barrier. bindWG counts the probe listeners (health, ready, admin,
	// startup); each signals Done once its socket is bound. listenersServing flips true once they
	// have ALL bound, so /readyz reports NOT ready during the bind window — k8s readiness means
	// "ready to serve", including the admin/probe paths, not merely "process spawned the goroutine".
	bindWG           sync.WaitGroup
	listenersServing atomic.Bool

	// Listener attempt accounting for waitForStop: lnStarted counts listeners
	// launched, lnBound those that bound; lnBoundCh wakes the waiter on a bind.
	// A failure is obeyed only once every listener has been attempted.
	lnStarted atomic.Int32
	lnBound   atomic.Int32
	lnBoundCh chan struct{}

	// signalReady, when non-nil, is closed by runSignalLoop once its signal
	// handlers are registered. It is a test-only readiness seam so tests can
	// send a signal without racing handler registration; nil in production.
	signalReady chan struct{}
}

// registerListenerReadinessCheck makes /readyz report NOT ready until every probe listener has bound
// its socket. Run registers it BEFORE the listeners start, so the very first /readyz already
// reflects the bind gate. The check reads listenersServing, flipped by startListenerReadinessGate.
func (s *Server) registerListenerReadinessCheck() {
	s.readiness.AddCheck("listeners", func() error {
		if !s.listenersServing.Load() {
			return errors.New("probe listeners binding")
		}
		return nil
	})
}

// startListenerReadinessGate flips listenersServing true once every probe listener has signalled its
// bind (bindWG). Run starts it AFTER the listeners are spawned, so the barrier count is complete
// before the wait. A listener that fails to bind never signals, so readiness stays not-ready — which
// is correct: the errCh error drives shutdown.
func (s *Server) startListenerReadinessGate() {
	go func() {
		s.bindWG.Wait()
		s.listenersServing.Store(true)
	}()
}

// addListener registers a listener about to be launched: it joins the readiness
// bind barrier and the attempt count waitForStop obeys failures against.
func (s *Server) addListener() {
	s.bindWG.Add(1)
	s.lnStarted.Add(1)
}

// listenerBound is each listener's onBound: it releases the readiness barrier,
// counts the listener as attempted, and wakes waitForStop.
func (s *Server) listenerBound() {
	s.lnBound.Add(1)
	s.bindWG.Done()
	select {
	case s.lnBoundCh <- struct{}{}:
	default:
	}
}

func NewServer(log *logging.Logger, cfg config.Config, opts ...Option) *Server {
	s := &Server{
		cfg:       cfg,
		logger:    log,
		readiness: probes.NewReadiness(),
		startup:   probes.NewStartup(),
		met:       metrics.New(),
		lnBoundCh: make(chan struct{}, 1),
	}
	s.runner = realRunner{onBound: s.listenerBound, h3Cert: s.h3Certificate}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// AddRoute registers a handler on the given port and URL pattern.
// Must be called before Run.
func (s *Server) AddRoute(port int, pattern string, h http.Handler) {
	s.registrars = append(s.registrars, router.RegistrarFunc(func(r *router.Router) {
		r.Handle(port, pattern, h)
	}))
}

// RegisterListener enrolls an embedder-owned listener in the /readyz barrier. The caller binds and
// serves its own socket and must call the returned onBound once it is bound; until then /readyz is
// not-ready and lists "listener:<name>". Must be called before Run.
func (s *Server) RegisterListener(name string) (onBound func()) {
	var bound atomic.Bool
	s.readiness.AddCheck("listener:"+name, func() error {
		if !bound.Load() {
			return errors.New("binding")
		}
		return nil
	})
	return func() { bound.Store(true) }
}

// applyRemoteSink tears down any existing remote sink, then builds and attaches
// a new one according to cfg. Also reconfigures the logger level and JSON flag.
// Safe to call on SIGHUP reload; sinkMu serialises sink lifecycle transitions.
func (s *Server) applyRemoteSink(cfg config.Config) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()

	// Cancel and discard the previous sink goroutine (if any). The previous
	// writer is closed on return — after the logger has been switched off it
	// (log writes hold the logger mutex, so nothing still writes to it).
	if s.sinkCancel != nil {
		s.sinkCancel()
		s.sinkCancel = nil
	}
	s.httpSink.Store(nil)
	prev := s.sinkOut
	s.sinkOut = nil
	defer closeSinkWriter(prev)

	// Without a new sink, an attached one must actually be detached: Reconfigure
	// ignores a nil Out, so restore stdout explicitly. With none attached, nil
	// keeps whatever output the embedder configured.
	var plain io.Writer
	if prev != nil {
		plain = os.Stdout
	}

	if !cfg.Logging.RemoteSink.Enabled || cfg.Logging.RemoteSink.Endpoint == "" {
		_ = s.logger.Reconfigure(logging.Config{Level: cfg.Logging.Level, JSON: cfg.Logging.JSON, Out: plain})
		return
	}

	w, httpSink, err := buildRemoteSinkFn(cfg.Logging.RemoteSink)
	if err != nil {
		s.logger.Warn("remote_sink_init_failed", map[string]any{"err": err.Error()})
		_ = s.logger.Reconfigure(logging.Config{Level: cfg.Logging.Level, JSON: cfg.Logging.JSON, Out: plain})
		return
	}
	s.sinkOut = w

	sinkCtx, cancel := context.WithCancel(s.runCtx)
	s.sinkCancel = cancel
	if httpSink != nil {
		s.httpSink.Store(httpSink)
		s.sinkWg.Add(1)
		go func() {
			defer s.sinkWg.Done()
			httpSink.Run(sinkCtx)
		}()
	}
	_ = s.logger.Reconfigure(logging.Config{
		Level: cfg.Logging.Level,
		JSON:  cfg.Logging.JSON,
		Out:   io.MultiWriter(os.Stdout, w),
	})
}

// applyOutboundSigner initialises or replaces the JWTSigner used to sign
// outbound sidecar requests. Called at startup (Fatal on error) and on
// SIGHUP reload (Warn on error, preserves the existing signer on failure).
func (s *Server) applyOutboundSigner(cfg config.Config) {
	if cfg.Authn.MyID == "" || cfg.Authn.MySignatureKeyFile == "" {
		s.signer.Store(nil)
		return
	}
	sg, err := mw.NewJWTSigner(cfg.Authn.MyID, cfg.Authn.MySignatureKeyFile)
	if err != nil {
		s.logger.Warn("jwt_signer_reload_failed", map[string]any{"err": err.Error()})
		return
	}
	s.signer.Store(sg)
}

// applyAuthnState precomputes the trusted signers list and stores it atomically
// so AuthnJWT middleware reads the latest state on every request after SIGHUP.
func (s *Server) applyAuthnState(cfg config.Config) {
	signers := mw.LoadTrustedSigners(cfg.Authn, s.logger)
	s.authn.Store(&authnSnapshot{signers: signers})
}

// applyTracing tears down any existing TracerProvider and sets up a new one
// according to cfg. Safe to call on SIGHUP reload; tracingMu serialises
// lifecycle transitions. Shutdown is synchronous so no goroutine is needed.
func (s *Server) applyTracing(cfg config.Config) {
	s.tracingMu.Lock()
	defer s.tracingMu.Unlock()

	tracing.Shutdown(s.expPtr.Load())
	s.expPtr.Store(nil)

	if !cfg.Tracing.OTLP.Enabled {
		return
	}

	exp, err := tracingSetupFn(cfg.Tracing.OTLP)
	if err != nil {
		s.logger.Warn("tracing_init_failed", map[string]any{"err": err.Error()})
		return
	}
	s.expPtr.Store(exp)
}

// buildRemoteSink constructs the appropriate remote log sink based on cfg.Protocol.
// Returns the io.Writer to pass to the logger and, for HTTP sinks only, the
// *logging.HTTPSink pointer needed for the drops-metric loop.
func buildRemoteSink(cfg config.RemoteSinkConfig) (io.Writer, *logging.HTTPSink, error) {
	if cfg.Protocol == "syslog" {
		w, err := logging.NewSyslogSink(cfg.Endpoint)
		if err != nil {
			return nil, nil, err
		}
		return w, nil, nil
	}
	sink := logging.NewHTTPSink(cfg.Endpoint, 1000, 5*time.Second)
	return sink, sink, nil
}

func (s *Server) Run(ctx context.Context) {
	if err := acmeValidate(s.cfg); err != nil {
		s.logger.Fatal("acme_config_invalid", map[string]any{"err": err.Error()})
	}
	applyMemoryLimit(s.cfg.Backpressure.HeapMaxBytes, s.logger)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.runCtx = ctx
	s.applyRemoteSink(s.cfg)
	s.applyTracing(s.cfg)
	defer func() { tracing.Shutdown(s.expPtr.Load()) }()

	if s.cfg.FIPS.Monitor {
		if err := keelfips.Check(); err != nil {
			s.logger.Fatal("fips_monitor_check_failed", map[string]any{"err": err.Error()})
		}
	}

	s.initStatsD()
	rt, mainHandler := s.buildMainRouter()
	healthMux := buildHealthMux()
	readyMux := buildReadyMux(s.readiness)
	adminMux := buildAdminMux(s.readiness, s.startup, s.metricsHandler(), s.ReloadHandler())

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	shutdown := lifecycle.NewShutdownOrchestrator(s.logger)
	go s.runSignalLoop(ctx)

	s.registerListenerReadinessCheck()
	s.startProbeListeners(ctx, shutdown, &wg, errCh, healthMux, readyMux, adminMux)
	acmeMgr := s.startACMEListener(ctx, shutdown, &wg, errCh)
	s.startMainListeners(ctx, shutdown, &wg, errCh, mainHandler, acmeMgr)
	s.startListenerReadinessGate()
	s.startSidecar(ctx, rt)
	s.startBackgroundLoops(ctx, &wg, acmeMgr)
	s.startup.Done()

	sigErr := s.waitForStop(ctx, shutdown, errCh)
	s.prestopSleep()
	s.drainListeners(cancel, &wg, errCh, sigErr)
}

// errListenerExited marks a listener that returned without error before shutdown.
var errListenerExited = errors.New("listener exited unexpectedly")

// waitForStop blocks until a shutdown signal or ctx end, or until listener
// failure must be obeyed. Listeners only return on their own after ctx is
// cancelled, so an early return (bind failure, Serve error) is a failure. Each
// failure is logged at once and recorded, but obeyed only after every listener
// has been attempted (bound or failed) — so all startup failures surface
// together rather than the first one masking the rest. The recorded failures
// go back on errCh (each receive freed a slot) for drainListeners to aggregate.
func (s *Server) waitForStop(ctx context.Context, shutdown *lifecycle.Orchestrator, errCh chan error) error {
	stopCtx, stop := context.WithCancel(ctx)
	defer stop()
	sigCh := make(chan error, 1)
	go func() { sigCh <- shutdown.WaitForStop(stopCtx) }()
	var failed []error
	defer func() {
		for _, err := range failed {
			errCh <- err
		}
	}()
	for {
		select {
		case sigErr := <-sigCh:
			return sigErr
		case err := <-errCh:
			if ctx.Err() != nil {
				// Shutting down: listeners return as ctx is cancelled, and this
				// receive can win the select against WaitForStop. Not a failure —
				// hand the result to drainListeners and finish the shutdown.
				errCh <- err
				return ctx.Err()
			}
			if err == nil {
				err = errListenerExited
			}
			s.logger.Error("listener_failed", map[string]any{"err": err.Error()})
			failed = append(failed, err)
		case <-s.lnBoundCh:
		}
		if len(failed) > 0 && int(s.lnBound.Load())+len(failed) >= int(s.lnStarted.Load()) {
			return nil
		}
	}
}

// initStatsD connects to the StatsD endpoint when configured. A dial error is
// logged as a warning and the server continues without StatsD.
func (s *Server) initStatsD() {
	if !s.cfg.Metrics.StatsD.Enabled || s.cfg.Metrics.StatsD.Endpoint == "" {
		return
	}
	sd, err := statsd.New(s.cfg.Metrics.StatsD.Endpoint, s.cfg.Metrics.StatsD.Prefix)
	if err != nil {
		s.logger.Warn("statsd_dial_failed", map[string]any{"err": err.Error()})
		return
	}
	s.sd = sd
}

// buildMainRouter assembles the application router, registers all routes, and
// wraps the handler with middleware. Returns the router (for sidecar wiring)
// and the fully-wrapped handler.
func (s *Server) buildMainRouter() (*router.Router, http.Handler) {
	rt := router.New()
	for _, r := range s.registrars {
		r.Register(rt)
	}
	s.registerDefaultRoutes(rt)
	s.applyAuthnState(s.cfg)
	return rt, s.wrapMain(rt.Handler())
}

// newACMEManager creates an ACME manager wired to the server logger.
func (s *Server) newACMEManager() *acme.Manager {
	mgr := acme.New()
	mgr.SetLogger(s.acmeLog)
	return mgr
}

// acmeLog is the ACME manager's error-logging callback. Extracted from
// newACMEManager so it can be exercised directly in unit tests.
func (s *Server) acmeLog(event string, fields map[string]any) {
	s.logger.Warn(event, fields)
}

// startProbeListeners launches goroutines for the health, ready, admin, and
// startup probe listeners according to the listener config.
func (s *Server) startProbeListeners(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, healthMux, readyMux, adminMux *http.ServeMux) {
	// Each enabled probe listener joins the readiness bind barrier: bindWG.Add(1) here, Done via the
	// onBound signal once its socket is bound, so /readyz gates on the admin/probe paths accepting
	// connections (not merely on the goroutine being spawned).
	if s.cfg.Listeners.Health.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Health.Port), healthMux, s.cfg, s.logger, s.listenerBound)
		}()
	}
	if s.cfg.Listeners.Ready.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Ready.Port), readyMux, s.cfg, s.logger, s.listenerBound)
		}()
	}
	if s.cfg.Listeners.Admin.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Admin.Port), adminMux, s.cfg, s.logger, s.listenerBound)
		}()
	}
	if s.cfg.Listeners.Startup.Enabled {
		startupMux := buildStartupMux(s.startup)
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Startup.Port), startupMux, s.cfg, s.logger, s.listenerBound)
		}()
	}
}

// startACMEListener starts the ACME certificate manager and its http-01
// challenge listener when ACME is enabled. Returns the manager (nil if
// disabled) for use by the HTTPS listener.
func (s *Server) startACMEListener(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error) *acme.Manager {
	if !s.cfg.TLS.ACME.Enabled {
		return nil
	}
	mgr := s.newACMEManager()
	go func() { _ = mgr.Start(ctx, s.cfg.TLS.ACME) }()
	wg.Add(1)
	s.addListener()
	go func() {
		defer wg.Done()
		errCh <- s.runner.serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.TLS.ACME.ChallengePort), mgr.HTTPHandler(s.cfg.Listeners.HTTPS.Port), s.cfg, s.logger)
	}()
	return mgr
}

// startHTTPSListener starts the HTTPS listener, using ACME GetCertificate when
// ACME is enabled, or a file-based CertLoader otherwise.
func (s *Server) startHTTPSListener(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, h http.Handler, acmeMgr *acme.Manager) {
	if !s.cfg.Listeners.HTTPS.Enabled {
		return
	}
	if s.cfg.TLS.ACME.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- s.runner.serveHTTPS(ctx, sd, config.AddrFromPort(s.cfg.Listeners.HTTPS.Port), h, s.cfg, nil, acmeMgr.GetCertificate, s.logger)
		}()
		return
	}
	if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
		s.logger.Fatal("https_no_tls_cert", map[string]any{"err": "cert_file and key_file required"})
	}
	loader, err := keeltls.NewCertLoader(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	if err != nil {
		s.logger.Fatal("tls_cert_load_failed", map[string]any{"err": err.Error()})
	}
	s.certLoader = loader
	wg.Add(1)
	s.addListener()
	go func() {
		defer wg.Done()
		errCh <- s.runner.serveHTTPS(ctx, sd, config.AddrFromPort(s.cfg.Listeners.HTTPS.Port), h, s.cfg, loader, nil, s.logger)
	}()
}

// startMainListeners launches the HTTP, HTTPS, and H3 application listeners.
func (s *Server) startMainListeners(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, h http.Handler, acmeMgr *acme.Manager) {
	if s.cfg.Listeners.HTTP.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- s.runner.serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.Listeners.HTTP.Port), h, s.cfg, s.logger)
		}()
	}
	s.startHTTPSListener(ctx, sd, wg, errCh, h, acmeMgr)
	if s.cfg.Listeners.H3.Enabled {
		if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
			s.logger.Fatal("h3_no_tls_cert", map[string]any{"err": "cert_file and key_file required"})
		}
		// H3 serves from the same CertLoader as HTTPS so SIGHUP rotation reaches
		// it; with HTTPS disabled, H3 creates the shared loader itself.
		if s.certLoader == nil {
			loader, err := keeltls.NewCertLoader(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
			if err != nil {
				s.logger.Fatal("tls_cert_load_failed", map[string]any{"err": err.Error()})
			}
			s.certLoader = loader
		}
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- s.runner.serveH3(ctx, config.AddrFromPort(s.cfg.Listeners.H3.Port), h, s.cfg, s.logger)
		}()
	}
}

// startBackgroundLoops starts the metric and monitor goroutines that run for
// the lifetime of the server.
func (s *Server) startBackgroundLoops(ctx context.Context, wg *sync.WaitGroup, acmeMgr *acme.Manager) {
	if s.cfg.Backpressure.SheddingEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mw.RunPressureLoop(ctx, s.readiness, s.cfg, s.logger)
		}()
	}
	if s.cfg.Listeners.HTTPS.Enabled {
		if s.cfg.TLS.ACME.Enabled {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runACMECertExpiryLoop(ctx, acmeMgr, s.met)
			}()
		} else if s.certLoader != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runCertExpiryLoop(ctx, s.cfg.TLS.CertFile, s.met)
			}()
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		runLogDropsLoop(ctx, s.httpSink.Load, s.met)
	}()
	if s.cfg.FIPS.Monitor {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runFIPSMonitorLoop(ctx, s.met)
		}()
	}
}

// prestopSleep pauses for the configured pre-stop duration, giving upstream
// load balancers time to drain connections before listeners close.
func (s *Server) prestopSleep() {
	d := s.Cfg().Timeouts.PrestopSleep.Duration
	if d <= 0 {
		return
	}
	s.logger.Info("prestop_sleep", map[string]any{"dur": d.String()})
	time.Sleep(d)
}

// drainListeners cancels remaining goroutines, waits for them to finish, and
// fatals on any listener or unexpected shutdown error.
func (s *Server) drainListeners(cancel context.CancelFunc, wg *sync.WaitGroup, errCh <-chan error, sigErr error) {
	cancel()
	wg.Wait()
	s.sinkWg.Wait()
	// Every listener has returned, so errCh holds all their results. Drain it
	// fully and exit with the aggregate of every failure — a clean nil from one
	// listener never masks another's error, and no failure hides behind the first.
	var failures []error
	for {
		select {
		case err := <-errCh:
			if err != nil {
				failures = append(failures, err)
			}
			continue
		default:
		}
		break
	}
	if len(failures) > 0 {
		s.logger.Fatal("listener_error", map[string]any{"err": errors.Join(failures...).Error(), "count": len(failures)})
		return
	}
	if sigErr != nil && !errors.Is(sigErr, context.Canceled) {
		s.logger.Fatal("shutdown_error", map[string]any{"err": sigErr.Error()})
	}
}

// registerDefaultRoutes adds the built-in "/" handler to rt for each enabled
// listener when useDefaultRegistrar is true.
func (s *Server) registerDefaultRoutes(rt *router.Router) {
	if !s.useDefaultRegistrar {
		return
	}
	defaultH := defaultKeeHandler()
	if s.cfg.Listeners.HTTP.Enabled {
		rt.Handle(s.cfg.Listeners.HTTP.Port, "/", defaultH)
	}
	if s.cfg.Listeners.HTTPS.Enabled {
		rt.Handle(s.cfg.Listeners.HTTPS.Port, "/", defaultH)
	}
	if s.cfg.Listeners.H3.Enabled {
		rt.Handle(s.cfg.Listeners.H3.Port, "/", defaultH)
	}
}

// startSidecar wires the reverse-proxy handler and upstream health probe when
// sidecar mode is configured. It is a no-op when sidecar is disabled or
// upstream_url is empty.
func (s *Server) startSidecar(ctx context.Context, rt *router.Router) {
	if !s.cfg.Sidecar.Enabled || s.cfg.Sidecar.UpstreamURL == "" {
		return
	}
	// Initialise the outbound signer (Fatal on startup error; Warn on reload error).
	if s.cfg.Authn.MyID != "" && s.cfg.Authn.MySignatureKeyFile != "" {
		sg, signerErr := mw.NewJWTSigner(s.cfg.Authn.MyID, s.cfg.Authn.MySignatureKeyFile)
		if signerErr != nil {
			s.logger.Fatal("jwt_signer_init_failed", map[string]any{"err": signerErr.Error()})
		}
		s.signer.Store(sg)
	}
	// signFn reads the atomic pointer on every request so SIGHUP key rotation
	// takes effect immediately without re-creating the sidecar proxy.
	signFn := newSignFn(&s.signer)
	h, err := sidecar.New(s.cfg, signFn)
	if err == nil {
		sidecar.StartHealthProbe(ctx, s.cfg.Sidecar, nil, s.readiness, s.logger)
		rt.Handle(s.cfg.Listeners.HTTP.Port, "/", h)
	} else {
		s.logger.Warn("sidecar_disabled", map[string]any{"err": err.Error()})
	}
}

// metricsHandler returns the Prometheus /metrics handler when
// cfg.Metrics.Prometheus is true, or a 404 handler otherwise.
func (s *Server) metricsHandler() http.Handler {
	if s.cfg.Metrics.Prometheus {
		return s.met.Handler()
	}
	return http.NotFoundHandler()
}

// Metrics returns the server's metrics store so an embedder can register custom
// Collectors whose series render on the built-in /metrics endpoint alongside
// keel's own, through the same responder. Register before Run.
func (s *Server) Metrics() *metrics.Metrics { return s.met }

func (s *Server) wrapMain(h http.Handler) http.Handler {
	if s.cfg.Security.OWASPHeaders {
		h = mw.OWASP(s.cfg, h)
	}
	if s.cfg.Backpressure.SheddingEnabled {
		h = mw.Shedding(s.readiness, h)
	}
	if s.cfg.Authn.Enabled {
		h = mw.AuthnJWT(s.cfg, func() []string {
			if sn := s.authn.Load(); sn != nil {
				return sn.signers
			}
			return nil
		}, h, s.logger)
	}
	if s.cfg.ExtAuthz.Enabled {
		h = mw.ExtAuthz(s.cfg, h, s.logger)
	}
	if s.cfg.Logging.AccessLog {
		h = mw.AccessLog(s.logger, h)
	}
	h = mw.RequestID(h)
	// OTelSpan is wrapped before TraceContext so it executes after TraceContext
	// at request time, giving it access to the trace/span IDs in r.Context().
	h = mw.OTelSpan(func() *tracing.Exporter { return s.expPtr.Load() }, h)
	h = mw.TraceContext(h)
	if s.cfg.Limits.MaxConcurrent > 0 {
		h = mw.ConcurrencyLimit(s.cfg, h)
	}
	if s.sd != nil {
		h = statsd.Instrument(s.sd, h)
	}
	h = s.met.Instrument(h)
	return h
}

// newHTTPServer builds an *http.Server with timeouts and limits from cfg.
func newHTTPServer(addr string, h http.Handler, cfg config.Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		MaxHeaderBytes:    cfg.Security.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.Timeouts.ReadHeader.Duration,
		ReadTimeout:       cfg.Timeouts.Read.Duration,
		WriteTimeout:      cfg.Timeouts.Write.Duration,
		IdleTimeout:       cfg.Timeouts.Idle.Duration,
	}
}

// buildHealthMux returns a *http.ServeMux with /healthz registered.
func buildHealthMux() *http.ServeMux {
	mux := http.NewServeMux()
	probes.RegisterHealth(mux)
	return mux
}

// buildReadyMux returns a *http.ServeMux with /readyz registered.
func buildReadyMux(r *probes.Readiness) *http.ServeMux {
	mux := http.NewServeMux()
	probes.RegisterReady(mux, r)
	return mux
}

// buildStartupMux returns a *http.ServeMux with /startupz registered.
func buildStartupMux(s *probes.Startup) *http.ServeMux {
	mux := http.NewServeMux()
	probes.RegisterStartup(mux, s)
	return mux
}

// buildAdminMux returns a *http.ServeMux with all admin routes registered.
func buildAdminMux(r *probes.Readiness, s *probes.Startup, metricsH, reloadH http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	probes.RegisterHealth(mux)
	probes.RegisterReady(mux, r)
	probes.RegisterStartup(mux, s)
	probes.RegisterFIPS(mux)
	probes.RegisterPProf(mux)
	mux.Handle("/metrics", metricsH)
	mux.Handle("/admin/reload", reloadH)
	mux.Handle("/version", version.Handler())
	return mux
}

// applyTLSCertSource sets tlsCfg.GetCertificate from loader or getCert.
func applyTLSCertSource(tlsCfg *cryptotls.Config, loader *keeltls.CertLoader, getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error)) {
	if loader != nil {
		tlsCfg.GetCertificate = loader.Get
	} else {
		tlsCfg.GetCertificate = getCert
	}
}

// defaultKeeHandler returns the built-in "/" handler used by useDefaultRegistrar.
func defaultKeeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL == nil || req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("keel: ok\n"))
	})
}

// newSignFn returns a function that signs outbound requests using the signer
// stored in the atomic pointer. If the pointer is nil, the request is unsigned.
func newSignFn(signer *atomic.Pointer[mw.JWTSigner]) func(*http.Request) error {
	return func(req *http.Request) error {
		if sg := signer.Load(); sg != nil {
			return sg.SignRequest(req)
		}
		return nil
	}
}

func serveHTTP(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, log *logging.Logger) error {
	return serveHTTPBound(ctx, shutdown, addr, h, cfg, log, nil)
}

// serveHTTPBound is serveHTTP with a bind signal: onBound (when non-nil) is invoked AFTER the socket
// is bound (net.Listen has succeeded) and BEFORE Serve. It lets a caller barrier on real bind
// completion — the readiness gate uses it so /readyz reports ready only once the probe listeners
// are actually accepting connections, not merely spawned.
func serveHTTPBound(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, log *logging.Logger, onBound func()) error {
	srv := newHTTPServer(addr, h, cfg)
	log.Debug("listener_binding", map[string]any{"addr": addr, "proto": "http/1.1"})
	ln, err := netListen("tcp", addr)
	if err != nil {
		log.Debug("listener_bind_failed", map[string]any{"addr": addr, "err": err.Error()})
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Info("listener_up", map[string]any{"addr": addr, "tls": false, "proto": "http/1.1"})
	if onBound != nil {
		onBound()
	}

	go func() {
		<-ctx.Done()
		drain := cfg.Timeouts.ShutdownDrain.Duration
		if drain == 0 {
			drain = 10 * time.Second
		}
		_ = shutdown.GracefulStop(drain, func(c context.Context) error { return srv.Shutdown(c) })
	}()

	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serveHTTPS(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, loader *keeltls.CertLoader, getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error), log *logging.Logger) error {
	return serveHTTPSBound(ctx, shutdown, addr, h, cfg, loader, getCert, log, nil)
}

func serveHTTPSBound(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, loader *keeltls.CertLoader, getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error), log *logging.Logger, onBound func()) error {
	tlsCfg := keeltls.BuildTLSConfig(cfg)
	applyTLSCertSource(tlsCfg, loader, getCert)
	srv := newHTTPServer(addr, h, cfg)
	srv.TLSConfig = tlsCfg
	httpx.ApplyHTTP2Policy(srv)

	log.Debug("listener_binding", map[string]any{"addr": addr, "proto": "https"})
	ln, err := netListen("tcp", addr)
	if err != nil {
		log.Debug("listener_bind_failed", map[string]any{"addr": addr, "err": err.Error()})
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Info("listener_up", map[string]any{"addr": addr, "tls": true, "proto": "https"})
	if onBound != nil {
		onBound()
	}

	go func() {
		<-ctx.Done()
		drain := cfg.Timeouts.ShutdownDrain.Duration
		if drain == 0 {
			drain = 10 * time.Second
		}
		_ = shutdown.GracefulStop(drain, func(c context.Context) error { return srv.Shutdown(c) })
	}()

	err = srv.Serve(cryptotls.NewListener(ln, tlsCfg))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// runTickLoop runs fn immediately then on every tick until ctx is done.
func runTickLoop(ctx context.Context, interval time.Duration, fn func()) {
	fn()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

func tickACMECertExpiry(mgr *acme.Manager, met *metrics.Metrics) {
	if secs, err := acmeCertExpiryFn(mgr); err == nil {
		met.SetCertExpiry(secs)
	}
}

func tickFileCertExpiry(certFile string, met *metrics.Metrics) {
	if secs, err := keeltls.CertExpirySeconds(certFile); err == nil {
		met.SetCertExpiry(secs)
	}
}

func tickLogDrops(getSink func() *logging.HTTPSink, met *metrics.Metrics) {
	if sink := getSink(); sink != nil {
		met.SetLogDrops(sink.DropsTotal())
	}
}

func tickFIPSMonitor(met *metrics.Metrics) {
	if err := keelfips.Check(); err != nil {
		met.IncFIPSMonitorFailure()
	}
}

func runACMECertExpiryLoop(ctx context.Context, mgr *acme.Manager, met *metrics.Metrics) {
	runTickLoop(ctx, time.Hour, func() { tickACMECertExpiry(mgr, met) })
}

func runCertExpiryLoop(ctx context.Context, certFile string, met *metrics.Metrics) {
	runTickLoop(ctx, time.Hour, func() { tickFileCertExpiry(certFile, met) })
}

func runLogDropsLoop(ctx context.Context, getSink func() *logging.HTTPSink, met *metrics.Metrics) {
	runTickLoop(ctx, 30*time.Second, func() { tickLogDrops(getSink, met) })
}

func runFIPSMonitorLoop(ctx context.Context, met *metrics.Metrics) {
	runTickLoop(ctx, time.Hour, func() { tickFIPSMonitor(met) })
}

func serveH3(ctx context.Context, addr string, h http.Handler, cfg config.Config, log *logging.Logger) error {
	return serveH3Bound(ctx, addr, h, cfg, log, nil)
}

func serveH3Bound(ctx context.Context, addr string, h http.Handler, cfg config.Config, log *logging.Logger, onBound func()) error {
	return serveH3With(ctx, addr, h, cfg, log, onBound, nil)
}

// h3Certificate serves HTTP/3 handshakes from the shared CertLoader. It is read
// per handshake, so it sees the loader startMainListeners installed (before the
// H3 goroutine starts) and every SIGHUP reload of it.
func (s *Server) h3Certificate(hello *cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error) {
	if s.certLoader == nil {
		return nil, errors.New("no TLS certificate loaded")
	}
	return s.certLoader.Get(hello)
}

// h3ListenPacket is a seam over net.ListenPacket for tests.
var h3ListenPacket = net.ListenPacket

// serveH3With binds the UDP socket first, so onBound (readiness) fires only once
// the port is held; then serves HTTP/3 with keel's TLS policy (TLS 1.3, FIPS
// curves — quic-go would otherwise build its own config) and the same header /
// idle limits as the TCP listeners. getCert is the shared cert source (rotated
// by SIGHUP); nil loads cfg.TLS.CertFile/KeyFile. Shutdown drains for
// cfg.Timeouts.ShutdownDrain like the TCP listeners.
func serveH3With(ctx context.Context, addr string, h http.Handler, cfg config.Config, log *logging.Logger, onBound func(),
	getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error)) error {
	log.Debug("listener_binding", map[string]any{"addr": addr, "proto": "h3"})
	conn, err := h3ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	defer conn.Close()
	log.Info("listener_up", map[string]any{"addr": addr, "tls": true, "proto": "h3"})
	if onBound != nil {
		onBound()
	}
	if ctx.Err() != nil {
		return nil
	}
	if getCert == nil {
		loader, err := keeltls.NewCertLoader(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("h3 cert: %w", err)
		}
		getCert = loader.Get
	}
	tlsCfg := keeltls.BuildTLSConfig(cfg)
	tlsCfg.GetCertificate = getCert
	srv := http3.NewServer(h, tlsCfg, http3.Limits{
		MaxHeaderBytes: cfg.Security.MaxHeaderBytes,
		IdleTimeout:    cfg.Timeouts.Idle.Duration,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(conn) }()

	select {
	case <-ctx.Done():
		drain := cfg.Timeouts.ShutdownDrain.Duration
		if drain <= 0 {
			drain = 10 * time.Second
		}
		c, cancel := context.WithTimeout(context.Background(), drain)
		defer cancel()
		_ = srv.Shutdown(c)
		return nil
	case err := <-errCh:
		return err
	}
}

// RunServer runs srv until ctx is cancelled.
// Fatal errors are handled internally by the server via its logger.
func RunServer(srv *Server, ctx context.Context) {
	srv.Run(ctx)
}

// closeSinkWriter closes a detached remote-sink writer that holds a resource
// (the syslog TCP/UDP connection). The HTTP sink has no Close; its goroutine is
// stopped by cancelling its context.
func closeSinkWriter(w io.Writer) {
	if c, ok := w.(io.Closer); ok {
		_ = c.Close()
	}
}
