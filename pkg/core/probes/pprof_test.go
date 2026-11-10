//go:build !no_pprof

package probes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterPProf(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPProf(mux)

	// Registration succeeds and the index handler responds.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pprof index code = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestPProf_RegisteredOnAdminMuxReturns200(t *testing.T) {
	adminMux := http.NewServeMux()
	RegisterPProf(adminMux)

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
