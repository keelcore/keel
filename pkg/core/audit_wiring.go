// pkg/core/audit_wiring.go
package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

// applyAudit brings the audit stream in line with cfg. Unchanged settings keep the open stream. A
// file stream continues the chain found on disk; an unreadable tail is an error, so a second
// chain never starts silently (Run stops; a reload keeps the stream it has). Before Run there is
// no context for a sink goroutine; Run applies the config again once it has one.
func (s *Server) applyAudit(cfg config.Config) error {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if s.runCtx == nil {
		return nil
	}
	if s.auditLog.Load() != nil && reflect.DeepEqual(s.auditCfg, cfg.Audit) {
		return nil
	}
	if !cfg.Audit.Enabled {
		s.stopAuditLocked()
		return nil
	}
	anchor, err := s.auditAnchor(cfg.Audit)
	if err != nil {
		return err
	}
	writers, closers, err := s.auditWriters(cfg.Audit)
	if err != nil {
		return err
	}
	s.stopAuditLocked()
	log := audit.New(io.MultiWriter(writers...), anchor, func(err error) {
		s.logger.Error("audit_write_failed", map[string]any{"err": err.Error()})
	})
	s.auditLog.Store(log)
	s.auditCfg, s.auditClosers = cfg.Audit, closers
	return nil
}

// auditAnchor is where the chain continues: the running log's position, or the file on disk.
func (s *Server) auditAnchor(cfg config.AuditConfig) (audit.Anchor, error) {
	if cfg.File == "" || cfg.File == s.auditCfg.File {
		if l := s.auditLog.Load(); l != nil {
			return l.Anchor(), nil
		}
	}
	if cfg.File == "" {
		return audit.Anchor{}, nil
	}
	anchor, err := audit.Resume(cfg.File)
	if err != nil {
		return audit.Anchor{}, fmt.Errorf("audit: resume the chain from %s: %w", cfg.File, err)
	}
	return anchor, nil
}

// auditWriters opens the configured sinks: the file first, so it holds every entry.
func (s *Server) auditWriters(cfg config.AuditConfig) ([]io.Writer, []func(), error) {
	var writers []io.Writer
	var closers []func()
	if cfg.File != "" {
		f, err := logging.NewRotatingFile(cfg.File, rotationPolicy(cfg.Rotation))
		if err != nil {
			return nil, nil, fmt.Errorf("audit: open %s: %w", cfg.File, err)
		}
		writers = append(writers, f)
		closers = append(closers, func() { _ = f.Close() })
	}
	if cfg.RemoteSink.Enabled && cfg.RemoteSink.Endpoint != "" {
		w, httpSink, err := buildRemoteSinkFn(cfg.RemoteSink)
		if err != nil {
			for _, c := range closers {
				c()
			}
			return nil, nil, fmt.Errorf("audit: remote sink: %w", err)
		}
		writers = append(writers, w)
		closers = append(closers, func() { closeSinkWriter(w) })
		if httpSink != nil {
			ctx, cancel := context.WithCancel(s.runCtx)
			s.sinkWg.Add(1)
			go func() {
				defer s.sinkWg.Done()
				httpSink.Run(ctx)
			}()
			closers = append(closers, cancel)
		}
	}
	return writers, closers, nil
}

func (s *Server) stopAuditLocked() {
	s.auditLog.Store(nil)
	for _, c := range s.auditClosers {
		c()
	}
	s.auditClosers, s.auditCfg = nil, config.AuditConfig{}
}

// closeAudit flushes and closes the stream, at shutdown.
func (s *Server) closeAudit() {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	s.stopAuditLocked()
}

// auditEvent records e when the stream is on.
func (s *Server) auditEvent(e audit.Event) {
	s.auditLog.Load().Record(e)
}

// wrapOIDC puts the browser login flow in front of authed. Requests that are not part of it go to
// bearer: the JWT path when authn is on, otherwise a 401. A flow that cannot start is fatal, as
// serving without it would leave the routes it protects open.
func (s *Server) wrapOIDC(authed, h http.Handler) http.Handler {
	var bearer http.Handler
	if s.cfg.Authn.Enabled {
		bearer = h
	}
	wrapped, err := mw.OIDC(s.cfg.Authn.OIDC, authed, bearer, s.logger, s.auditEvent)
	if err != nil {
		s.logger.Fatal("oidc_init_failed", map[string]any{"err": err.Error()})
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		})
	}
	return wrapped
}

// startAudit applies the audit config at startup. An audit stream that cannot start is fatal:
// serving without it would leave security events unrecorded.
func (s *Server) startAudit() {
	if err := s.applyAudit(s.cfg); err != nil {
		s.logger.Fatal("audit_init_failed", map[string]any{"err": err.Error()})
	}
}
