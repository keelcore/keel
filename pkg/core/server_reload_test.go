// server_reload_test.go — white-box tests for Reload and ReloadHandler (package core).
package core

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// ---------------------------------------------------------------------------
// Reload — invalid config
// ---------------------------------------------------------------------------

// Reload returns an error and leaves the running config unchanged when the
// config file is unparseable.
func TestReload_InvalidConfig_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("{\nnot valid yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths(cfgPath, ""))

	err := s.Reload()
	if err == nil {
		t.Error("expected error for invalid config file, got nil")
	}
}

// Reload with missing config file returns an error.
func TestReload_MissingConfigFile_ReturnsError(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths("/nonexistent-keel-cfg.yaml", ""))

	err := s.Reload()
	if err == nil {
		t.Error("expected error for missing config file, got nil")
	}
}

// ---------------------------------------------------------------------------
// Cfg — returns consistent snapshot
// ---------------------------------------------------------------------------

// Cfg returns the current config without holding the lock.
func TestCfg_ReturnsCurrent(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Logging: config.LoggingConfig{Level: "debug"},
	}
	s := NewServer(log, cfg)

	got := s.Cfg()
	if got.Logging.Level != "debug" {
		t.Errorf("expected level 'debug', got %q", got.Logging.Level)
	}
}

// ---------------------------------------------------------------------------
// ReloadHandler — non-POST returns 405
// ---------------------------------------------------------------------------

func TestReloadHandler_GetReturns405(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/reload", nil))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// Reload succeeds when the config file is valid (returns nil error).
func TestReload_ValidConfig_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	// Minimal valid YAML: empty mapping inherits all defaults.
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths(cfgPath, ""))

	if err := s.Reload(); err != nil {
		t.Errorf("expected nil error for valid config, got: %v", err)
	}
}

// ReloadHandler POST with a valid config file returns 200.
func TestReloadHandler_Post_ValidConfig_Returns200(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths(cfgPath, ""))

	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// ReloadHandler POST with a missing config file returns 422.
func TestReloadHandler_Post_MissingConfig_Returns422(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths("/nonexistent-reload-cfg.yaml", ""))

	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Reload — TLS cert reload failure (certLoader != nil branch)
// ---------------------------------------------------------------------------

// Reload with an active certLoader logs a warning when the cert paths are
// invalid; the overall Reload still returns nil (warn is non-fatal).
func TestReload_CertLoader_ReloadFails_Warns(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	// Reference non-existent cert/key so certLoader.Reload will fail.
	yaml := fmt.Sprintf("tls:\n  cert_file: %s\n  key_file: %s\n",
		filepath.Join(dir, "missing.crt"),
		filepath.Join(dir, "missing.key"))
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{}, WithConfigPaths(cfgPath, ""))
	// Inject a zero-value CertLoader (white-box): Reload will fail on missing files.
	s.certLoader = &keeltls.CertLoader{}

	if err := s.Reload(); err != nil {
		t.Errorf("expected nil from Reload (warn is non-fatal), got %v", err)
	}
}
