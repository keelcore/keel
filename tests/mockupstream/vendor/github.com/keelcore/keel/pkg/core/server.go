// pkg/core/server.go
package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
	keelfips "github.com/keelcore/keel/pkg/core/fips"
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

	// reloadMu serializes Reload against Run's startup and against itself.
	// draining is set once shutdown begins; /readyz then reports NOT ready.
	reloadMu sync.Mutex
	draining atomic.Bool

	// Backpressure (applyBackpressure): the memory guard sheds while headroom
	// is low; bpCancel/bpWg restart the pressure and guard loops on reload.
	memGuard *mw.MemoryGuard
	bpMu     sync.Mutex
	bpCancel context.CancelFunc
	bpWg     sync.WaitGroup

	// signalReady, when non-nil, is closed by runSignalLoop once its signal
	// handlers are registered. It is a test-only readiness seam so tests can
	// send a signal without racing handler registration; nil in production.
	signalReady chan struct{}
}

func NewServer(log *logging.Logger, cfg config.Config, opts ...Option) *Server {
	s := &Server{
		cfg:       cfg,
		logger:    log,
		readiness: probes.NewReadiness(),
		startup:   probes.NewStartup(),
		met:       metrics.New(),
		memGuard:  mw.NewMemoryGuard(),
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

func (s *Server) Run(ctx context.Context) {
	// Startup reads s.cfg throughout; a reload (SIGHUP or /admin/reload) arriving
	// meanwhile waits on reloadMu until startup completes instead of racing it.
	s.reloadMu.Lock()
	s.logIgnoredEnv(s.cfg)
	if err := acmeValidate(s.cfg); err != nil {
		s.logger.Fatal("acme_config_invalid", map[string]any{"err": err.Error()})
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.runCtx = ctx
	s.applyBackpressure(s.cfg)
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
	adminMux := s.adminMux()

	var wg sync.WaitGroup
	errCh := make(chan error, maxListeners)
	shutdown := lifecycle.NewShutdownOrchestrator(s.logger)
	defer shutdown.Stop()
	go s.runSignalLoop(ctx)

	s.registerListenerReadinessCheck()
	s.registerDrainingReadinessCheck()
	// The sidecar route is registered before any listener serves the router,
	// so no request (and no readiness) precedes it.
	s.startSidecar(ctx, rt)
	s.startProbeListeners(ctx, shutdown, &wg, errCh, healthMux, readyMux, adminMux)
	acmeMgr := s.startACMEListener(ctx, shutdown, &wg, errCh)
	s.startMainListeners(ctx, shutdown, &wg, errCh, mainHandler, acmeMgr)
	s.startListenerReadinessGate()
	s.startBackgroundLoops(ctx, &wg, acmeMgr)
	s.startup.Done()
	s.reloadMu.Unlock()

	sigErr := s.waitForStop(ctx, shutdown, errCh)
	// Report NOT ready before the prestop sleep so load balancers drain this
	// instance while listeners still serve in-flight and late-routed requests.
	s.draining.Store(true)
	s.logger.Info("readiness_draining", nil)
	s.prestopSleep()
	s.drainListeners(cancel, &wg, errCh, sigErr)
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
		s.signer.Store(sg.WithAudience(s.cfg.Sidecar.UpstreamAudience))
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
	// Wrapped inner-first, so requests run in the reverse order: … → access log →
	// OWASP headers/limits → shedding → AuthnJWT → ExtAuthz → handler. Rejections
	// (401/403/503) carry the OWASP headers and are access-logged; the external
	// authorizer is only called for authenticated requests.
	if s.cfg.ExtAuthz.Enabled {
		h = mw.ExtAuthz(s.cfg, h, s.logger)
	}
	if s.cfg.Authn.Enabled {
		// Signers and the trusted_ids allowlist both come from the authn snapshot,
		// so a reload changes them atomically. Without a snapshot there are no
		// signers, so every token is rejected before the allowlist is consulted.
		h = mw.AuthnJWT(s.cfg, func() []string {
			if sn := s.authn.Load(); sn != nil {
				return sn.signers
			}
			return nil
		}, h, s.logger, mw.WithTrustedIDs(func() map[string]struct{} { return s.authn.Load().trustedIDSet() }))
	}
	if s.cfg.Backpressure.SheddingEnabled {
		h = mw.Shedding(s.readiness, h)
	}
	// Body caps and the read timeout always apply; OWASP adds the headers on top.
	if s.cfg.Security.OWASPHeaders {
		h = mw.OWASP(s.cfg, h)
	} else {
		h = mw.Limits(s.cfg, h)
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
	// Shed (503) early, before any other work, while memory headroom is low.
	h = mw.ShedOnLowMemory(s.memGuard, h)
	if s.sd != nil {
		h = statsd.Instrument(s.sd, h)
	}
	h = s.met.Instrument(h)
	return h
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
	mux.Handle("/metrics", metricsH)
	mux.Handle("/admin/reload", reloadH)
	mux.Handle("/version", version.Handler())
	return mux
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

// RunServer runs srv until ctx is cancelled.
// Fatal errors are handled internally by the server via its logger.
func RunServer(srv *Server, ctx context.Context) {
	srv.Run(ctx)
}
