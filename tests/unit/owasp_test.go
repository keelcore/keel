package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/mw"
)

func TestOWASP_SetsHeaders(t *testing.T) {
	cfg := config.Config{
		Security: config.SecurityConfig{MaxRequestBodyBytes: 1024},
	}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Header().Get("x-content-type-options") != "nosniff" {
		t.Fatalf("missing security header")
	}
}
