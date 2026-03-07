package unit

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	core "github.com/keelcore/keel/pkg/core"
)

func TestReload_ValidConfig_UpdatesFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("logging:\n  json: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := core.NewServer(core.WithConfigPaths(cfgPath, ""))
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() error: %v", err)
	}
	if !s.Cfg().Logging.JSON {
		t.Error("expected Logging.JSON=true after reload, got false")
	}
}

func TestReload_InvalidConfig_KeepsOldConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("logging:\n  json: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := core.NewServer(core.WithConfigPaths(cfgPath, ""))
	if err := s.Reload(); err != nil {
		t.Fatalf("initial Reload() error: %v", err)
	}
	before := s.Cfg()

	// Overwrite with unparseable YAML.
	if err := os.WriteFile(cfgPath, []byte("{\nnot: valid: yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err == nil {
		t.Fatal("expected error from invalid config, got nil")
	}

	after := s.Cfg()
	if after.Logging.JSON != before.Logging.JSON {
		t.Error("config should be unchanged after failed reload")
	}
}

func TestAdminReload_ValidConfig_Returns200(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	s := core.NewServer(core.WithConfigPaths(cfgPath, ""))
	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminReload_InvalidConfig_Returns422(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "keel.yaml")
	if err := os.WriteFile(cfgPath, []byte("{\nnot valid yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := core.NewServer(core.WithConfigPaths(cfgPath, ""))
	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminReload_WrongMethod_Returns405(t *testing.T) {
	s := core.NewServer()
	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/reload", nil))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}