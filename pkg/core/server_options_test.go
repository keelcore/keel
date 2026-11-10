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
)

// ---------------------------------------------------------------------------
// WithConfig
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// WithLogger
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// WithRegistrar
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// WithDefaultRegistrar
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// WithReadinessCheck
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ReloadHandler — POST success path
// ---------------------------------------------------------------------------

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
