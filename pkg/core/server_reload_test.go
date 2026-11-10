// server_reload_test.go — white-box tests for Reload and ReloadHandler (package core).
package core

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Reload fails closed on an unparseable env override and keeps the running config.
func TestReload_MalformedEnv_KeepsConfig(t *testing.T) {
	t.Setenv("KEEL_AUTHN_ENABLED", "flase")
	running := config.Defaults()
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, running)

	if err := s.Reload(); err == nil {
		t.Fatal("expected error for KEEL_AUTHN_ENABLED=flase, got nil")
	}
	if s.Cfg().Authn.Enabled != running.Authn.Enabled {
		t.Error("a failed reload changed the running config")
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
	yaml := fmt.Sprintf("keel:\n  tls:\n    cert_file: %s\n    key_file: %s\n",
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

// A failed reload must not echo internal error detail (paths, parse errors) to
// the caller; the body is generic and the detail goes to the log.
func TestReloadHandler_Post_FailureBodyIsGeneric(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{}, WithConfigPaths("/nonexistent/keel-reload-secret-path.yaml", ""))
	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "reload failed\n" {
		t.Errorf("body = %q, want generic %q", body, "reload failed\n")
	}
}

func TestAudit_ConfigReloadAndAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	s.cfgPaths = [2]string{filepath.Join(t.TempDir(), "missing.yaml"), ""}
	if err := s.Reload(); err == nil {
		t.Fatal("want a reload error")
	}
	text := auditText(t, path)
	if !strings.Contains(text, `"event":"config_reload"`) || !strings.Contains(text, `"outcome":"fail"`) {
		t.Fatalf("audit %q", text)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	s.ReloadHandler().ServeHTTP(httptest.NewRecorder(), r)
	if !strings.Contains(auditText(t, path), `"event":"admin_reload"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

// A reload arriving while startup holds reloadMu waits for startup to finish.
func TestReload_WaitsForStartup(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{},
		WithConfigPaths("/nonexistent/keel-reload-wait.yaml", ""))
	s.reloadMu.Lock() // as Run holds it during startup
	done := make(chan error, 1)
	go func() { done <- s.Reload() }()
	select {
	case <-done:
		t.Fatal("Reload ran while startup held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	s.reloadMu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Reload did not proceed after startup released the lock")
	}
}

// ReloadHandler returns 200 on successful POST when config paths produce valid config.
func TestReloadHandler_PostSuccess_Returns200(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("keel:\n  logging:\n    json: true\n"), 0o600); err != nil {
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
