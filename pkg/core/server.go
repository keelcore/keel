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
	"runtime/debug"
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

// authnSnapshot holds the precomputed authn state used by AuthnJWT middleware.
// It is rebuilt on every SIGHUP reload via applyAuthnState.
type authnSnapshot struct {
	signers []string
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

	// Remote sink lifecycle. runCtx is set once in Run and used by
	// applyRemoteSink to derive sink goroutine contexts so they are
	// cancelled on both SIGHUP reload and process shutdown.
	// sinkWg tracks the active sink goroutine so Run waits for its final
	// flush to complete before the process exits.
	runCtx     context.Context
	sinkMu     sync.Mutex
	sinkCancel context.CancelFunc
	sinkWg     sync.WaitGroup
	httpSink   atomic.Pointer[logging.HTTPSink]

	// Outbound signer: updated atomically on SIGHUP so the sidecar proxy
	// picks up the new key without being re-created.
	signer atomic.Pointer[mw.JWTSigner]
	authn  atomic.Pointer[authnSnapshot]

	// OTLP tracing: tracingMu serialises Setup/Shutdown across concurrent
	// SIGHUP reloads; expPtr is read per-request via the OTelSpan middleware.
	tracingMu sync.Mutex
	expPtr    atomic.Pointer[tracing.Exporter]
}

func NewServer(log *logging.Logger, cfg config.Config, opts ...Option) *Server {
	s := &Server{
		cfg:       cfg,
		logger:    log,
		readiness: probes.NewReadiness(),
		startup:   probes.NewStartup(),
		met:       metrics.New(),
	}
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

// applyRemoteSink tears down any existing remote sink, then builds and attaches
// a new one according to cfg. Also reconfigures the logger level and JSON flag.
// Safe to call on SIGHUP reload; sinkMu serialises sink lifecycle transitions.
func (s *Server) applyRemoteSink(cfg config.Config) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()

	// Cancel and discard the previous sink goroutine (if any).
	if s.sinkCancel != nil {
		s.sinkCancel()
		s.sinkCancel = nil
	}
	s.httpSink.Store(nil)

	if !cfg.Logging.RemoteSink.Enabled || cfg.Logging.RemoteSink.Endpoint == "" {
		_ = s.logger.Reconfigure(logging.Config{Level: cfg.Logging.Level, JSON: cfg.Logging.JSON})
		return
	}

	w, httpSink, err := buildRemoteSink(cfg.Logging.RemoteSink)
	if err != nil {
		s.logger.Warn("remote_sink_init_failed", map[string]any{"err": err.Error()})
		_ = s.logger.Reconfigure(logging.Config{Level: cfg.Logging.Level, JSON: cfg.Logging.JSON})
		return
	}

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

	exp, err := tracing.Setup(cfg.Tracing.OTLP)
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
	if err := acme.Validate(s.cfg); err != nil {
		s.logger.Fatal("acme_config_invalid", map[string]any{"err": err.Error()})
	}
	if s.cfg.Backpressure.HeapMaxBytes > 0 {
		debug.SetMemoryLimit(s.cfg.Backpressure.HeapMaxBytes)
	}

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

	s.startProbeListeners(ctx, shutdown, &wg, errCh, healthMux, readyMux, adminMux)
	acmeMgr := s.startACMEListener(ctx, shutdown, &wg, errCh)
	s.startMainListeners(ctx, shutdown, &wg, errCh, mainHandler, acmeMgr)
	s.startSidecar(ctx, rt)
	s.startBackgroundLoops(ctx, &wg, acmeMgr)
	s.startup.Done()

	sigErr := shutdown.WaitForStop(ctx)
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
	mgr.SetLogger(func(event string, fields map[string]any) {
		s.logger.Warn(event, fields)
	})
	return mgr
}

// startProbeListeners launches goroutines for the health, ready, admin, and
// startup probe listeners according to the listener config.
func (s *Server) startProbeListeners(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, healthMux, readyMux, adminMux *http.ServeMux) {
	if s.cfg.Listeners.Health.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Health.Port), healthMux, s.cfg, s.logger)
		}()
	}
	if s.cfg.Listeners.Ready.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Ready.Port), readyMux, s.cfg, s.logger)
		}()
	}
	if s.cfg.Listeners.Admin.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Admin.Port), adminMux, s.cfg, s.logger)
		}()
	}
	if s.cfg.Listeners.Startup.Enabled {
		startupMux := buildStartupMux(s.startup)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.Listeners.Startup.Port), startupMux, s.cfg, s.logger)
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
	go func() {
		defer wg.Done()
		errCh <- serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.TLS.ACME.ChallengePort), mgr.HTTPHandler(s.cfg.Listeners.HTTPS.Port), s.cfg, s.logger)
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
		go func() {
			defer wg.Done()
			errCh <- serveHTTPS(ctx, sd, config.AddrFromPort(s.cfg.Listeners.HTTPS.Port), h, s.cfg, nil, acmeMgr.GetCertificate, s.logger)
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
	go func() {
		defer wg.Done()
		errCh <- serveHTTPS(ctx, sd, config.AddrFromPort(s.cfg.Listeners.HTTPS.Port), h, s.cfg, loader, nil, s.logger)
	}()
}

// startMainListeners launches the HTTP, HTTPS, and H3 application listeners.
func (s *Server) startMainListeners(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, h http.Handler, acmeMgr *acme.Manager) {
	if s.cfg.Listeners.HTTP.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, sd, config.AddrFromPort(s.cfg.Listeners.HTTP.Port), h, s.cfg, s.logger)
		}()
	}
	s.startHTTPSListener(ctx, sd, wg, errCh, h, acmeMgr)
	if s.cfg.Listeners.H3.Enabled {
		if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
			s.logger.Fatal("h3_no_tls_cert", map[string]any{"err": "cert_file and key_file required"})
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveH3(ctx, config.AddrFromPort(s.cfg.Listeners.H3.Port), h, s.cfg, s.logger)
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
	if sigErr != nil {
		cancel()
	}
	select {
	case err := <-errCh:
		cancel()
		wg.Wait()
		s.sinkWg.Wait()
		if err != nil {
			s.logger.Fatal("listener_error", map[string]any{"err": err.Error()})
		}
	default:
		cancel()
		wg.Wait()
		s.sinkWg.Wait()
		if sigErr != nil && !errors.Is(sigErr, context.Canceled) {
			s.logger.Fatal("shutdown_error", map[string]any{"err": sigErr.Error()})
		}
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
	srv := newHTTPServer(addr, h, cfg)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Info("listener_up", map[string]any{"addr": addr, "tls": false, "proto": "http/1.1"})

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
	tlsCfg := keeltls.BuildTLSConfig(cfg)
	applyTLSCertSource(tlsCfg, loader, getCert)
	srv := newHTTPServer(addr, h, cfg)
	srv.TLSConfig = tlsCfg
	httpx.ApplyHTTP2Policy(srv)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Info("listener_up", map[string]any{"addr": addr, "tls": true, "proto": "https"})

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
	if secs, err := mgr.CertExpiry(); err == nil {
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
	tlsCfg := keeltls.BuildTLSConfig(cfg)
	srv := http3.New(addr, h, tlsCfg)
	log.Info("listener_up", map[string]any{"addr": addr, "tls": true, "proto": "h3"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	}()

	select {
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
