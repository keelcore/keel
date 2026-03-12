package core

import (
	"fmt"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

// Reload re-reads the configuration from the paths supplied via
// WithConfigPaths, validates it, and—if valid—applies live fields
// (stored config, TLS certificate). On any error the running
// configuration is left unchanged.
func (s *Server) Reload() error {
	cfg, err := config.Load(s.cfgPaths[0], s.cfgPaths[1])
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()

	// Apply logging config and restart the remote sink if its config changed.
	s.applyRemoteSink(cfg)
	// Re-initialise the outbound signer (Warn on error, preserves old signer).
	s.applyOutboundSigner(cfg)
	// Reload trusted signers list (trusted_signers_file is re-read from disk).
	s.applyAuthnState(cfg)
	// Reinitialise OTLP tracing if endpoint or enabled flag changed.
	s.applyTracing(cfg)

	// Reload TLS certificate if a loader is active.
	if s.certLoader != nil && cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
		if err := s.certLoader.Reload(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != nil {
			s.logger.Warn("tls_cert_reload_failed", map[string]any{"err": err.Error()})
		}
	}

	s.logger.Info("config_reloaded", nil)
	return nil
}

// Cfg returns a consistent snapshot of the server's current configuration.
func (s *Server) Cfg() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// ReloadHandler returns an http.Handler for POST /admin/reload.
// A successful reload responds 200; an invalid config responds 422.
func (s *Server) ReloadHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.Reload(); err != nil {
			s.logger.Warn("admin_reload_failed", map[string]any{"err": err.Error()})
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
