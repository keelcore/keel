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
	runCtx     context.Context
	sinkMu     sync.Mutex
	sinkCancel context.CancelFunc
	httpSink   atomic.Pointer[logging.HTTPSink]

	// Outbound signer: updated atomically on SIGHUP so the sidecar proxy
	// picks up the new key without being re-created.
	signer atomic.Pointer[mw.JWTSigner]
	authn  atomic.Pointer[authnSnapshot]
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
		go httpSink.Run(sinkCtx)
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

	// Create the inner cancellable context before the remote sink so sink
	// goroutines share the same lifetime as the listeners.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.runCtx = ctx

	// Apply logging level/format and start the remote sink (if configured).
	s.applyRemoteSink(s.cfg)

	// FIPS monitor startup gate: fatal if fips.monitor is enabled and FIPS
	// runtime mode is not active. Checked before any listener starts so the
	// binary fails closed rather than serving traffic in a non-FIPS state.
	if s.cfg.FIPS.Monitor {
		if err := keelfips.Check(); err != nil {
			s.logger.Fatal("fips_monitor_check_failed", map[string]any{"err": err.Error()})
		}
	}

	// StatsD client.
	if s.cfg.Metrics.StatsD.Enabled && s.cfg.Metrics.StatsD.Endpoint != "" {
		if sd, err := statsd.New(s.cfg.Metrics.StatsD.Endpoint, s.cfg.Metrics.StatsD.Prefix); err == nil {
			s.sd = sd
		} else {
			s.logger.Warn("statsd_dial_failed", map[string]any{"err": err.Error()})
		}
	}

	// Main router: ONLY application routes, each registered with an explicit fixed port.
	mainRT := router.New()
	for _, r := range s.registrars {
		r.Register(mainRT)
	}
	if s.useDefaultRegistrar {
		defaultH := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL == nil || req.URL.Path != "/" {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("content-type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("keel: ok\n"))
		})
		if s.cfg.Listeners.HTTP.Enabled {
			mainRT.Handle(s.cfg.Listeners.HTTP.Port, "/", defaultH)
		}
		if s.cfg.Listeners.HTTPS.Enabled {
			mainRT.Handle(s.cfg.Listeners.HTTPS.Port, "/", defaultH)
		}
		if s.cfg.Listeners.H3.Enabled {
			mainRT.Handle(s.cfg.Listeners.H3.Port, "/", defaultH)
		}
	}

	// Probes: separate muxes, served ONLY on probe/admin listeners.
	healthMux := http.NewServeMux()
	probes.RegisterHealth(healthMux)

	readyMux := http.NewServeMux()
	probes.RegisterReady(readyMux, s.readiness)

	adminMux := http.NewServeMux()
	probes.RegisterHealth(adminMux)
	probes.RegisterReady(adminMux, s.readiness)
	probes.RegisterStartup(adminMux, s.startup)
	probes.RegisterFIPS(adminMux)
	probes.RegisterPProf(adminMux)
	adminMux.Handle("/metrics", s.metricsHandler())
	adminMux.Handle("/admin/reload", s.ReloadHandler())
	adminMux.Handle("/version", version.Handler())

	s.applyAuthnState(s.cfg)
	mainHandler := s.wrapMain(mainRT.Handler())

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 8)
	)

	shutdown := lifecycle.NewShutdownOrchestrator(s.logger)
	go s.runSignalLoop(ctx)

	// --- Probe / Admin listeners ---
	if s.cfg.Listeners.Health.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.Health.Port), healthMux, s.cfg, s.logger)
		}()
	}

	if s.cfg.Listeners.Ready.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.Ready.Port), readyMux, s.cfg, s.logger)
		}()
	}

	// Admin: enabled independently of health/ready listeners.
	if s.cfg.Listeners.Admin.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.Admin.Port), adminMux, s.cfg, s.logger)
		}()
	}

	// Startup probe listener (separate port; /startupz only).
	if s.cfg.Listeners.Startup.Enabled {
		startupMux := http.NewServeMux()
		probes.RegisterStartup(startupMux, s.startup)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.Startup.Port), startupMux, s.cfg, s.logger)
		}()
	}

	// --- Main listeners ---
	if s.cfg.Listeners.HTTP.Enabled {
		httpH := http.Handler(mainHandler)
		if s.cfg.TLS.ACME.Enabled {
			acmeMgr := acme.New()
			go func() { _ = acmeMgr.Start(ctx, s.cfg.TLS.ACME) }()
			httpH = acmeMgr.HTTPHandler(s.cfg.Listeners.HTTPS.Port)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.HTTP.Port), httpH, s.cfg, s.logger)
		}()
	}

	if s.cfg.Listeners.HTTPS.Enabled {
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
			errCh <- serveHTTPS(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.HTTPS.Port), mainHandler, s.cfg, loader, s.logger)
		}()
	}

	if s.cfg.Listeners.H3.Enabled {
		if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
			s.logger.Fatal("h3_no_tls_cert", map[string]any{"err": "cert_file and key_file required"})
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveH3(ctx, config.AddrFromPort(s.cfg.Listeners.H3.Port), mainHandler, s.cfg, s.logger)
		}()
	}

	// Sidecar route registration.
	if s.cfg.Sidecar.Enabled && s.cfg.Sidecar.UpstreamURL != "" {
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
		signFn := func(req *http.Request) error {
			if sg := s.signer.Load(); sg != nil {
				return sg.SignRequest(req)
			}
			return nil
		}
		h, err := sidecar.New(s.cfg, signFn)
		if err == nil {
			sidecar.StartHealthProbe(ctx, s.cfg.Sidecar, nil, s.readiness, s.logger)
			mainRT.Handle(s.cfg.Listeners.HTTP.Port, "/", h)
		} else {
			s.logger.Warn("sidecar_disabled", map[string]any{"err": err.Error()})
		}
	}

	if s.cfg.Backpressure.SheddingEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mw.RunPressureLoop(ctx, s.readiness, s.cfg, s.logger)
		}()
	}

	// Cert expiry metric loop: update keel_tls_cert_expiry_seconds while running.
	if s.cfg.Listeners.HTTPS.Enabled && s.certLoader != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runCertExpiryLoop(ctx, s.cfg.TLS.CertFile, s.met)
		}()
	}

	// Log drops metric loop: always started so a reload-added sink is
	// automatically tracked without restarting the goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		runLogDropsLoop(ctx, s.httpSink.Load, s.met)
	}()

	// FIPS monitor loop: only started when fips.monitor is enabled.
	// Logs a warning and increments a metric on each failed check (hourly).
	if s.cfg.FIPS.Monitor {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runFIPSMonitorLoop(ctx, s.met)
		}()
	}

	// All initialization complete; mark the startup probe ready.
	s.startup.Done()

	sigErr := shutdown.WaitForStop(ctx)

	if d := s.Cfg().Timeouts.PrestopSleep.Duration; d > 0 {
		s.logger.Info("prestop_sleep", map[string]any{"dur": d.String()})
		time.Sleep(d)
	}

	if sigErr != nil {
		cancel()
	}

	select {
	case err := <-errCh:
		cancel()
		wg.Wait()
		if err != nil {
			s.logger.Fatal("listener_error", map[string]any{"err": err.Error()})
		}
	default:
		cancel()
		wg.Wait()
		if sigErr != nil && !errors.Is(sigErr, context.Canceled) {
			s.logger.Fatal("shutdown_error", map[string]any{"err": sigErr.Error()})
		}
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

func serveHTTP(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, log *logging.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		MaxHeaderBytes:    cfg.Security.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.Timeouts.ReadHeader.Duration,
		ReadTimeout:       cfg.Timeouts.Read.Duration,
		WriteTimeout:      cfg.Timeouts.Write.Duration,
		IdleTimeout:       cfg.Timeouts.Idle.Duration,
	}
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

func serveHTTPS(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, loader *keeltls.CertLoader, log *logging.Logger) error {
	tlsCfg := keeltls.BuildTLSConfig(cfg)
	tlsCfg.GetCertificate = loader.Get
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		MaxHeaderBytes:    cfg.Security.MaxHeaderBytes,
		ReadHeaderTimeout: cfg.Timeouts.ReadHeader.Duration,
		ReadTimeout:       cfg.Timeouts.Read.Duration,
		WriteTimeout:      cfg.Timeouts.Write.Duration,
		IdleTimeout:       cfg.Timeouts.Idle.Duration,
		TLSConfig:         tlsCfg,
	}
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

func runCertExpiryLoop(ctx context.Context, certFile string, met *metrics.Metrics) {
	if secs, err := keeltls.CertExpirySeconds(certFile); err == nil {
		met.SetCertExpiry(secs)
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if secs, err := keeltls.CertExpirySeconds(certFile); err == nil {
				met.SetCertExpiry(secs)
			}
		}
	}
}

func runLogDropsLoop(ctx context.Context, getSink func() *logging.HTTPSink, met *metrics.Metrics) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if sink := getSink(); sink != nil {
				met.SetLogDrops(sink.DropsTotal())
			}
		}
	}
}

func runFIPSMonitorLoop(ctx context.Context, met *metrics.Metrics) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := keelfips.Check(); err != nil {
				met.IncFIPSMonitorFailure()
			}
		}
	}
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
