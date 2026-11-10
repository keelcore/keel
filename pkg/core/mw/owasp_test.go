//go:build !no_owasp

package mw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

// OWASP sets security headers on every response.
func TestOWASP_SetsSecurityHeaders(t *testing.T) {
	cfg := config.Config{
		Security: config.SecurityConfig{
			OWASPHeaders: true,
			HSTSMaxAge:   63072000,
		},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := OWASP(cfg, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("x-content-type-options") != "nosniff" {
		t.Error("expected x-content-type-options: nosniff")
	}
	if rr.Header().Get("x-frame-options") != "DENY" {
		t.Error("expected x-frame-options: DENY")
	}
}

// OWASP does not set HSTS on plain HTTP (r.TLS == nil).
func TestOWASP_NoHSTSOnPlainHTTP(t *testing.T) {
	cfg := config.Config{
		Security: config.SecurityConfig{HSTSMaxAge: 63072000},
	}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// r.TLS is nil for httptest requests → HSTS not set.
	h.ServeHTTP(rr, req)

	if rr.Header().Get("strict-transport-security") != "" {
		t.Error("expected no HSTS header on plain HTTP")
	}
}

// OWASP limits the request body when MaxRequestBodyBytes > 0.
func TestOWASP_LimitsRequestBody(t *testing.T) {
	cfg := config.Config{
		Security: config.SecurityConfig{MaxRequestBodyBytes: 10},
	}
	var bodyLen int
	h := OWASP(cfg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b := make([]byte, 100)
		n, _ := r.Body.Read(b)
		bodyLen = n
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello world this is a long body"))
	h.ServeHTTP(rr, req)

	if bodyLen > 10 {
		t.Errorf("expected body limited to 10 bytes, read %d", bodyLen)
	}
}
