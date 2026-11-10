package core

import (
	"io"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/router"
)

func TestWithConfig_SetsConfig(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Logging: config.LoggingConfig{Level: "debug"}}
	s := NewServer(log, config.Config{}, WithConfig(cfg))
	if s.cfg.Logging.Level != "debug" {
		t.Errorf("expected level 'debug', got %q", s.cfg.Logging.Level)
	}
}

func TestWithLogger_SetsLogger(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithLogger(log))
	if s.logger == nil {
		t.Error("expected non-nil logger after WithLogger")
	}
}

func TestWithRegistrar_AppendsRegistrar(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	called := false
	reg := router.RegistrarFunc(func(r *router.Router) {
		called = true
	})
	s := NewServer(log, config.Config{}, WithRegistrar(reg))
	if len(s.registrars) != 1 {
		t.Errorf("expected 1 registrar, got %d", len(s.registrars))
	}
	// Exercise the registrar.
	rt := router.New()
	s.registrars[0].Register(rt)
	if !called {
		t.Error("expected registrar to be called during Register")
	}
}

func TestWithDefaultRegistrar_SetsFlag(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithDefaultRegistrar())
	if !s.useDefaultRegistrar {
		t.Error("expected useDefaultRegistrar=true after WithDefaultRegistrar")
	}
}

func TestWithReadinessCheck_RegistersCheck(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithReadinessCheck("test-dep", func() error { return nil }))
	// The check is registered via s.readiness; verify IsReady passes.
	ok, _ := s.readiness.IsReady()
	if !ok {
		t.Error("expected IsReady=true after registering a passing check")
	}
}
