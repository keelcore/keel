// server_options_test.go — tests for Option constructors (package core).
package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/router"
)

// ---------------------------------------------------------------------------
// WithConfig
// ---------------------------------------------------------------------------

func TestWithConfig_SetsConfig(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Logging: config.LoggingConfig{Level: "debug"}}
	s := NewServer(log, config.Config{}, WithConfig(cfg))
	if s.cfg.Logging.Level != "debug" {
		t.Errorf("expected level 'debug', got %q", s.cfg.Logging.Level)
	}
}

// ---------------------------------------------------------------------------
// WithLogger
// ---------------------------------------------------------------------------

func TestWithLogger_SetsLogger(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithLogger(log))
	if s.logger == nil {
		t.Error("expected non-nil logger after WithLogger")
	}
}

// ---------------------------------------------------------------------------
// WithRegistrar
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// WithDefaultRegistrar
// ---------------------------------------------------------------------------

func TestWithDefaultRegistrar_SetsFlag(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithDefaultRegistrar())
	if !s.useDefaultRegistrar {
		t.Error("expected useDefaultRegistrar=true after WithDefaultRegistrar")
	}
}

// ---------------------------------------------------------------------------
// WithReadinessCheck
// ---------------------------------------------------------------------------

func TestWithReadinessCheck_RegistersCheck(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithReadinessCheck("test-dep", func() error { return nil }))
	// The check is registered via s.readiness; verify IsReady passes.
	ok, _ := s.readiness.IsReady()
	if !ok {
		t.Error("expected IsReady=true after registering a passing check")
	}
}

// ---------------------------------------------------------------------------
// ReloadHandler — POST success path
// ---------------------------------------------------------------------------

// ReloadHandler returns 200 on successful POST when config paths produce valid config.
func TestReloadHandler_PostSuccess_Returns200(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("logging:\n  json: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths(cfgPath, ""))

	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 on successful reload, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ReloadHandler returns 422 when config is invalid (Reload returns error).
func TestReloadHandler_PostBadConfig_Returns422(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths("/nonexistent.yaml", ""))

	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 on failed reload, got %d", rr.Code)
	}
}
