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
