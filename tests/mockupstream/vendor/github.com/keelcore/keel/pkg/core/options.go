package core

import (
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/router"
)

type Option func(*Server)

func WithConfig(cfg config.Config) Option {
	return func(s *Server) { s.cfg = cfg }
}

func WithLogger(log *logging.Logger) Option {
	return func(s *Server) { s.logger = log }
}

func WithRegistrar(r router.Registrar) Option {
	return func(s *Server) { s.registrars = append(s.registrars, r) }
}

func WithDefaultRegistrar() Option {
	return func(s *Server) { s.useDefaultRegistrar = true }
}

// WithConfigPaths records the config and secrets file paths used by Reload.
// configPath and secretsPath may be empty strings if unused.
func WithConfigPaths(configPath, secretsPath string) Option {
	return func(s *Server) {
		s.cfgPaths[0] = configPath
		s.cfgPaths[1] = secretsPath
	}
}

// WithReadinessCheck registers a named dependency check evaluated on /readyz.
// fn should return a non-nil error when the dependency is unhealthy.
func WithReadinessCheck(name string, fn func() error) Option {
	return func(s *Server) {
		s.readiness.AddCheck(name, fn)
	}
}
