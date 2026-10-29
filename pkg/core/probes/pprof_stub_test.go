//go:build no_pprof

package probes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterPProf_NoPProfRegistersNothing(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPProf(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if PProfCompiled || rec.Code != http.StatusNotFound {
		t.Fatalf("PProfCompiled=%v code=%d, want false 404", PProfCompiled, rec.Code)
	}
}
