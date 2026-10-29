// pkg/core/server_listeners.go
package core

import (
	"context"
	cryptotls "crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
	"github.com/keelcore/keel/pkg/core/http3"
	"github.com/keelcore/keel/pkg/core/httpx"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

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

// registerDrainingReadinessCheck makes /readyz report NOT ready once shutdown has
// begun (draining). It is a check rather than Readiness.Set(false) because the
// readiness flag is also written by the sidecar health probe, which would flip
// it back to ready mid-drain; a failing check cannot be overridden.
func (s *Server) registerDrainingReadinessCheck() {
	s.readiness.AddCheck("draining", func() error {
		if s.draining.Load() {
			return errors.New("shutting down")
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

// maxListeners sizes errCh: one slot per listener kind (health, ready, startup,
// admin, ACME challenge, HTTP, HTTPS, H3), each sending exactly one result.
// Raise it when adding a listener kind, or a send blocks shutdown.
const maxListeners = 8

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

// startProbeListeners launches goroutines for the health, ready, admin, and
// startup probe listeners according to the listener config.
func (s *Server) startProbeListeners(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, healthMux, readyMux, adminMux *http.ServeMux) {
	// Goroutines below use this snapshot, never s.cfg: a reload must not race them.
	cfg := s.cfg
	// Each enabled probe listener joins the readiness bind barrier: bindWG.Add(1) here, Done via the
	// onBound signal once its socket is bound, so /readyz gates on the admin/probe paths accepting
	// connections (not merely on the goroutine being spawned).
	if s.cfg.Listeners.Health.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(cfg.Listeners.Health.Port), healthMux, cfg, s.logger, s.listenerBound)
		}()
	}
	if s.cfg.Listeners.Ready.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(cfg.Listeners.Ready.Port), readyMux, cfg, s.logger, s.listenerBound)
		}()
	}
	if s.cfg.Listeners.Admin.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(cfg.Listeners.Admin.Port), adminMux, cfg, s.logger, s.listenerBound)
		}()
	}
	if s.cfg.Listeners.Startup.Enabled {
		startupMux := buildStartupMux(s.startup)
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- serveHTTPBound(ctx, sd, config.AddrFromPort(cfg.Listeners.Startup.Port), startupMux, cfg, s.logger, s.listenerBound)
		}()
	}
}

// startACMEListener starts the ACME certificate manager and its http-01
// challenge listener when ACME is enabled. Returns the manager (nil if
// disabled) for use by the HTTPS listener.
func (s *Server) startACMEListener(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error) *acme.Manager {
	// Goroutines below use this snapshot, never s.cfg: a reload must not race them.
	cfg := s.cfg
	if !s.cfg.TLS.ACME.Enabled {
		return nil
	}
	mgr := s.newACMEManager()
	go func() {
		if err := mgr.Start(ctx, cfg.TLS.ACME); err != nil {
			s.logger.Error("acme_failed", map[string]any{"err": err.Error()})
		}
	}()
	wg.Add(1)
	s.addListener()
	go func() {
		defer wg.Done()
		errCh <- s.runner.serveHTTP(ctx, sd, config.AddrFromPort(cfg.TLS.ACME.ChallengePort), mgr.HTTPHandler(cfg.Listeners.HTTPS.Port, cfg.TLS.ACME.Domains), cfg, s.logger)
	}()
	return mgr
}

// startHTTPSListener starts the HTTPS listener, using ACME GetCertificate when
// ACME is enabled, or a file-based CertLoader otherwise.
func (s *Server) startHTTPSListener(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, h http.Handler, acmeMgr *acme.Manager) {
	// Goroutines below use this snapshot, never s.cfg: a reload must not race them.
	cfg := s.cfg
	if !s.cfg.Listeners.HTTPS.Enabled {
		return
	}
	if s.cfg.TLS.ACME.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- s.runner.serveHTTPS(ctx, sd, config.AddrFromPort(cfg.Listeners.HTTPS.Port), h, cfg, nil, acmeMgr.GetCertificate, s.logger)
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
		errCh <- s.runner.serveHTTPS(ctx, sd, config.AddrFromPort(cfg.Listeners.HTTPS.Port), h, cfg, loader, nil, s.logger)
	}()
}

// startMainListeners launches the HTTP, HTTPS, and H3 application listeners.
func (s *Server) startMainListeners(ctx context.Context, sd *lifecycle.Orchestrator, wg *sync.WaitGroup, errCh chan<- error, h http.Handler, acmeMgr *acme.Manager) {
	// Goroutines below use this snapshot, never s.cfg: a reload must not race them.
	cfg := s.cfg
	if s.cfg.Listeners.HTTP.Enabled {
		wg.Add(1)
		s.addListener()
		go func() {
			defer wg.Done()
			errCh <- s.runner.serveHTTP(ctx, sd, config.AddrFromPort(cfg.Listeners.HTTP.Port), h, cfg, s.logger)
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
			errCh <- s.runner.serveH3(ctx, config.AddrFromPort(cfg.Listeners.H3.Port), h, cfg, s.logger)
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
	s.bpWg.Wait()
	if s.sd != nil {
		_ = s.sd.Close() // no request can reach statsd.Instrument any more
	}
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

// applyTLSCertSource sets tlsCfg.GetCertificate from loader or getCert.
func applyTLSCertSource(tlsCfg *cryptotls.Config, loader *keeltls.CertLoader, getCert func(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error)) {
	if loader != nil {
		tlsCfg.GetCertificate = loader.Get
	} else {
		tlsCfg.GetCertificate = getCert
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
	if err := applyClientCA(tlsCfg, cfg.TLS.ClientCAFile); err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	applyTLSCertSource(tlsCfg, loader, getCert)
	srv := newHTTPServer(addr, h, cfg)
	srv.TLSConfig = tlsCfg
	httpx.ApplyHTTP2Policy(srv)
	// ALPN must offer h2 or no client can negotiate it over our own TLS listener;
	// NextProtos follows the build policy (h2 + http/1.1, or http/1.1 under no_h2).
	tlsCfg.NextProtos = httpx.NextProtos()

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
	if err := applyClientCA(tlsCfg, cfg.TLS.ClientCAFile); err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
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

// applyClientCA enables inbound mTLS when tls.client_ca_file is set: the
// handshake fails unless the client presents a certificate chaining to it.
func applyClientCA(tlsCfg *cryptotls.Config, caFile string) error {
	if caFile == "" {
		return nil
	}
	pool, err := keeltls.LoadClientCAs(caFile)
	if err != nil {
		return err
	}
	tlsCfg.ClientCAs = pool
	tlsCfg.ClientAuth = cryptotls.RequireAndVerifyClientCert
	return nil
}
