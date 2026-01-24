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
}

func NewServer(opts ...Option) *Server {
    s := &Server{
        cfg:       config.Config{},
        readiness: probes.NewReadiness(),
        logger:    logging.New(logging.Config{JSON: true}),
    }
    for _, opt := range opts {
        opt(s)
    }
    s.logger = logging.New(logging.Config{JSON: s.cfg.LogJSON})
    return s
}

func (s *Server) Run(ctx context.Context) error {
    if s.cfg.HeapMaxBytes > 0 {
        debug.SetMemoryLimit(s.cfg.HeapMaxBytes)
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
    // Admin can include BOTH probes if Admin is enabled and dedicated probe listeners are disabled.
    // This is still “registered only on admin ports” (fixed port), never on main ports.
    probes.RegisterHealth(adminMux)
    probes.RegisterReady(adminMux, s.readiness)

    mainHandler := s.wrapMain(mainRT.Handler())

    var (
        wg     sync.WaitGroup
        errCh  = make(chan error, 8)
        cancel context.CancelFunc
    )
    ctx, cancel = context.WithCancel(ctx)
    defer cancel()

    shutdown := lifecycle.NewShutdownOrchestrator(s.logger)

    // --- Probes / Admin listeners (explicit fixed ports, env-gated) ---
    if s.cfg.Health.Enabled {
        wg.Add(1)
        go func() {
            defer wg.Done()
            errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Health.Port), healthMux, s.cfg, s.logger)
        }()
    }

    if s.cfg.Ready.Enabled {
        wg.Add(1)
        go func() {
            defer wg.Done()
            errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Ready.Port), readyMux, s.cfg, s.logger)
        }()
    }

    // Optional combined admin listener (only if enabled AND probe listeners are not enabled).
    if s.cfg.Admin.Enabled && !s.cfg.Health.Enabled && !s.cfg.Ready.Enabled {
        wg.Add(1)
        go func() {
            defer wg.Done()
            errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.Admin.Port), adminMux, s.cfg, s.logger)
        }()
    }

    // --- Main listeners (explicit fixed ports, env-gated) ---
    if s.cfg.HTTP.Enabled {
        wg.Add(1)
        go func() {
            defer wg.Done()
            errCh <- serveHTTP(ctx, shutdown, config.AddrFromPort(s.cfg.HTTP.Port), mainHandler, s.cfg, s.logger)
        }()
    }

    if s.cfg.HTTPS.Enabled {
        if s.cfg.TLSCertFile == "" || s.cfg.TLSKeyFile == "" {
            return errors.New("https enabled but KEEL_TLS_CERT/KEEL_TLS_KEY not set")
        }
        wg.Add(1)
        go func() {
            defer wg.Done()
            errCh <- serveHTTPS(ctx, shutdown, config.AddrFromPort(s.cfg.HTTPS.Port), mainHandler, s.cfg, s.logger)
        }()
    }

    if s.cfg.H3.Enabled {
        if s.cfg.TLSCertFile == "" || s.cfg.TLSKeyFile == "" {
            return errors.New("http3 enabled but KEEL_TLS_CERT/KEEL_TLS_KEY not set")
        }
        wg.Add(1)
        go func() {
            defer wg.Done()
            errCh <- serveH3(ctx, config.AddrFromPort(s.cfg.H3.Port), mainHandler, s.cfg, s.logger)
        }()
    }

    // Sidecar route registration is still explicit on a fixed port (router enforces port binding).
    if s.cfg.SidecarEnabled && s.cfg.UpstreamURL != "" {
        h, err := sidecar.ReverseProxy(s.cfg.UpstreamURL)
        if err == nil {
            // Example: sidecar published on HTTP port only (fixed).
            mainRT.Handle(s.cfg.HTTP.Port, "/", h)
        } else {
            s.logger.Warn("sidecar_disabled", map[string]any{"err": err.Error()})
        }
    }

    if s.cfg.SheddingEnabled {
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
    if s.cfg.SecurityHeadersEnabled {
        h = mw.OWASP(s.cfg, h)
    }
    if s.cfg.SheddingEnabled {
        h = mw.Shedding(s.readiness, h)
    }
    if s.cfg.AuthnEnabled {
        h = mw.AuthnJWT(s.cfg, h, s.logger)
    }
    return h
}

func serveHTTP(ctx context.Context, shutdown *lifecycle.Orchestrator, addr string, h http.Handler, cfg config.Config, log *logging.Logger) error {
    srv := &http.Server{
        Addr:              addr,
        Handler:           h,
        ReadHeaderTimeout: cfg.ReadHeaderTimeout,
        ReadTimeout:       cfg.ReadTimeout,
        WriteTimeout:      cfg.WriteTimeout,
        IdleTimeout:       cfg.IdleTimeout,
    }
    ln, err := net.Listen("tcp", addr)
    if err != nil {
        return fmt.Errorf("listen %s: %w", addr, err)
    }
    log.Info("listener_up", map[string]any{"addr": addr, "tls": false, "proto": "http/1.1"})

    go func() {
        <-ctx.Done()
        _ = shutdown.GracefulStop(func(c context.Context) error { return srv.Shutdown(c) })
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
        ReadHeaderTimeout: cfg.ReadHeaderTimeout,
        ReadTimeout:       cfg.ReadTimeout,
        WriteTimeout:      cfg.WriteTimeout,
        IdleTimeout:       cfg.IdleTimeout,
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
        _ = shutdown.GracefulStop(func(c context.Context) error { return srv.Shutdown(c) })
    }()

    err = srv.ServeTLS(ln, cfg.TLSCertFile, cfg.TLSKeyFile)
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
        errCh <- srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
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
