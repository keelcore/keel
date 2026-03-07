// pkg/core/server.go
package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/http3"
	"github.com/keelcore/keel/pkg/core/httpx"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/pkg/core/router"
	"github.com/keelcore/keel/pkg/core/sidecar"
	"github.com/keelcore/keel/pkg/core/tls"
)

type Server struct {
	cfg        config.Config
	registrars []router.Registrar

	readiness *probes.Readiness
	logger    *logging.Logger
	met       *metrics.Metrics
}

func NewServer(opts ...Option) *Server {
	s := &Server{
		cfg:       config.Config{},
		readiness: probes.NewReadiness(),
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
	if s.cfg.Backpressure.HeapMaxBytes > 0 {
		debug.SetMemoryLimit(s.cfg.Backpressure.HeapMaxBytes)
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
	adminMux.Handle("/metrics", s.met.Handler())

	mainHandler := s.wrapMain(mainRT.Handler())

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 8)
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	shutdown := lifecycle.NewShutdownOrchestrator(s.logger)

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

	// Admin: only if enabled AND dedicated probe listeners are not both enabled.
	if s.cfg.Listeners.Admin.Enabled && !s.cfg.Listeners.Health.Enabled && !s.cfg.Listeners.Ready.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.Admin.Port), adminMux, s.cfg, s.logger)
		}()
	}

	// --- Main listeners ---
	if s.cfg.Listeners.HTTP.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.HTTP.Port), mainHandler, s.cfg, s.logger)
		}()
	}

	if s.cfg.Listeners.HTTPS.Enabled {
		if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
			return errors.New("https enabled but TLS cert/key not configured")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- serveHTTPS(ctx, shutdown, config.AddrFromPort(s.cfg.Listeners.HTTPS.Port), mainHandler, s.cfg, s.logger)
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
		h, err := sidecar.ReverseProxy(s.cfg.Sidecar.UpstreamURL)
		if err == nil {
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

func serveHTTPS(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, log *logging.Logger) error {
	tlsCfg := tls.BuildTLSConfig(cfg)
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

	err = srv.ServeTLS(ln, cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serveH3(ctx context.Context, addr string, h http.Handler, cfg config.Config, log *logging.Logger) error {
	tlsCfg := tls.BuildTLSConfig(cfg)
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
