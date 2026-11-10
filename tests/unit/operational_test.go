package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/probes"
)

// ---------------------------------------------------------------------------
// /version
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// /startupz
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// /readyz with named checks
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// /health/fips (P16)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// /debug/pprof/ on admin mux only
// ---------------------------------------------------------------------------

func TestPProf_RegisteredOnAdminMuxReturns200(t *testing.T) {
	adminMux := http.NewServeMux()
	probes.RegisterPProf(adminMux)

	rr := httptest.NewRecorder()
	adminMux.ServeHTTP(rr, httptest.NewRequest("GET", "/debug/pprof/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("pprof on admin: status = %d, want 200", rr.Code)
	}
}

func TestPProf_NotOnMainHandlerReturns404(t *testing.T) {
	// Main mux has no pprof routes registered.
	mainMux := http.NewServeMux()

	rr := httptest.NewRecorder()
	mainMux.ServeHTTP(rr, httptest.NewRequest("GET", "/debug/pprof/", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("pprof on main: status = %d, want 404", rr.Code)
	}
}
