package core

import (
    "github.com/keelcore/keel/pkg/config"
    "github.com/keelcore/keel/pkg/core/router"
)

type Option func(*Server)

func WithConfig(cfg config.Config) Option {
    return func(s *Server) { s.cfg = cfg }
}

func WithRegistrar(r router.Registrar) Option {
    return func(s *Server) { s.registrars = append(s.registrars, r) }
}

func WithDefaultRegistrar() Option {
    return WithRegistrar(router.DefaultRegistrar())
}
