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
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
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

type Server struct {
	cfg      config.Config
	cfgMu    sync.RWMutex
	cfgPaths [2]string // [configPath, secretsPath] for Reload

	registrars []router.Registrar
	readiness  *probes.Readiness
	startup    *probes.Startup
	logger     *logging.Logger
	met        *metrics.Metrics
	sd         *statsd.Client
	certLoader *keeltls.CertLoader
}

func NewServer(opts ...Option) *Server {
	s := &Server{
		cfg:       config.Config{},
		readiness: probes.NewReadiness(),
		startup:   probes.NewStartup(),
		logger:    logging.New(logging.Config{JSON: true}),
		met:       metrics.New(),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.logger = logging.New(logging.Config{JSON: s.cfg.Logging.JSON})
	return s
}

func (s *Server) Run(ctx context.Context) error {
	if err := acme.Validate(s.cfg); err != nil {
		return err
	}

	if s.cfg.Backpressure.HeapMaxBytes > 0 {
		debug.SetMemoryLimit(s.cfg.Backpressure.HeapMaxBytes)
	}

	// Remote log sink: attach to logger if configured.
	var sink *logging.HTTPSink
	if s.cfg.Logging.RemoteSink.Enabled && s.cfg.Logging.RemoteSink.Endpoint != "" {
		sink = logging.NewHTTPSink(s.cfg.Logging.RemoteSink.Endpoint, 1000, 5*time.Second)
		go sink.Run(ctx)
		s.logger = logging.New(logging.Config{
			JSON: s.cfg.Logging.JSON,
			Out:  io.MultiWriter(os.Stdout, sink),
		})
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
	adminMux.Handle("/metrics", s.met.Handler())
	adminMux.Handle("/admin/reload", s.ReloadHandler())
	adminMux.Handle("/version", version.Handler())

	mainHandler := s.wrapMain(mainRT.Handler())

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 8)
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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
			return errors.New("https enabled but TLS cert/key not configured")
		}
		loader, err := keeltls.NewCertLoader(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("load TLS cert: %w", err)
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
			return errors.New("http3 enabled but TLS cert/key not configured")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveH3(ctx, config.AddrFromPort(s.cfg.Listeners.H3.Port), mainHandler, s.cfg, s.logger)
		}()
	}

	// Sidecar route registration.
	if s.cfg.Sidecar.Enabled && s.cfg.Sidecar.UpstreamURL != "" {
		h, err := sidecar.New(s.cfg)
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

	// Log drops metric loop: update keel_log_drops_total while running.
	if sink != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLogDropsLoop(ctx, sink, s.met)
		}()
	}

	// All initialization complete; mark the startup probe ready.
	s.startup.Done()

	sigErr := shutdown.WaitForStop(ctx)
	if sigErr != nil {
		cancel()
	}

	select {
	case err := <-errCh:
		cancel()
		wg.Wait()
		return err
	default:
		cancel()
		wg.Wait()
		return sigErr
	}
}

func (s *Server) wrapMain(h http.Handler) http.Handler {
	if s.cfg.Security.OWASPHeaders {
		h = mw.OWASP(s.cfg, h)
	}
	if s.cfg.Backpressure.SheddingEnabled {
		h = mw.Shedding(s.readiness, h)
	}
	if s.cfg.Authn.Enabled {
		h = mw.AuthnJWT(s.cfg, h, s.logger)
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

func runLogDropsLoop(ctx context.Context, sink *logging.HTTPSink, met *metrics.Metrics) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			met.SetLogDrops(sink.DropsTotal())
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
