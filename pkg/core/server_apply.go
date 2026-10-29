// pkg/core/server_apply.go
package core

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/tracing"
)

// authnSnapshot holds the precomputed authn state used by AuthnJWT middleware.
// It is rebuilt on every reload (SIGHUP or /admin/reload) via applyAuthnState:
// trusted signers and the trusted_ids allowlist both take effect without restart.
type authnSnapshot struct {
	signers    []string
	trustedIDs map[string]struct{}
}

// trustedIDSet returns the snapshot's allowlist; nil-safe (no snapshot: nil).
func (sn *authnSnapshot) trustedIDSet() map[string]struct{} {
	if sn == nil {
		return nil
	}
	return sn.trustedIDs
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

	// Before Run there is no context to bind a sink goroutine to; Run applies
	// the (reloaded) config again once it has one.
	if s.runCtx == nil || !cfg.Logging.RemoteSink.Enabled || cfg.Logging.RemoteSink.Endpoint == "" {
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
	s.signer.Store(sg.WithAudience(cfg.Sidecar.UpstreamAudience))
}

// applyAuthnState precomputes the trusted signers list and stores it atomically
// so AuthnJWT middleware reads the latest state on every request after SIGHUP.
func (s *Server) applyAuthnState(cfg config.Config) {
	signers := mw.LoadTrustedSigners(cfg.Authn, s.logger)
	s.authn.Store(&authnSnapshot{signers: signers, trustedIDs: mw.TrustedIDSet(cfg.Authn.TrustedIDs)})
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

// applyBackpressure applies backpressure.* at startup and on every reload: the
// Go memory limit, the heap-watermark pressure loop (shedding_enabled), and the
// memory-headroom guard (min_headroom_bytes). Running loops are stopped first —
// two loops must never write the same flag — and the backpressure flag is reset
// so a loop stopped while latched cannot leave it stuck.
func (s *Server) applyBackpressure(cfg config.Config) {
	s.bpMu.Lock()
	defer s.bpMu.Unlock()
	if s.bpCancel != nil {
		s.bpCancel()
		s.bpWg.Wait()
	}
	s.readiness.Set(true)
	applyMemoryLimit(cfg.Backpressure.HeapMaxBytes, s.logger)
	if s.runCtx == nil {
		return // not running yet (Reload before Run): Run starts the loops
	}

	ctx, cancel := context.WithCancel(s.runCtx)
	s.bpCancel = cancel
	if cfg.Backpressure.SheddingEnabled {
		s.bpWg.Add(1)
		go func() {
			defer s.bpWg.Done()
			mw.RunPressureLoop(ctx, s.readiness, cfg, s.logger)
		}()
	}
	s.bpWg.Add(1)
	go func() {
		defer s.bpWg.Done()
		mw.RunMemoryGuard(ctx, s.memGuard, cfg.Backpressure.MinHeadroomBytes, s.logger)
	}()
}

// closeSinkWriter closes a detached remote-sink writer that holds a resource
// (the syslog TCP/UDP connection). The HTTP sink has no Close; its goroutine is
// stopped by cancelling its context.
func closeSinkWriter(w io.Writer) {
	if c, ok := w.(io.Closer); ok {
		_ = c.Close()
	}
}
