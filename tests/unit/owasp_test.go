package unit

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestOWASP_AllFiveHeadersOnHTTP(t *testing.T) {
	h := mw.OWASP(config.Config{}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	for _, name := range []string{
		"x-content-type-options",
		"x-frame-options",
		"referrer-policy",
		"content-security-policy",
		"permissions-policy",
	} {
		if rr.Header().Get(name) == "" {
			t.Errorf("missing OWASP header: %s", name)
		}
	}
}

func TestOWASP_HSTSAbsentOnHTTP(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{HSTSMaxAge: 63072000}}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil) // r.TLS == nil
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if v := rr.Header().Get("strict-transport-security"); v != "" {
		t.Errorf("HSTS must be absent on plain HTTP, got: %s", v)
	}
}

func TestOWASP_HSTSPresentOnHTTPS(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{HSTSMaxAge: 63072000}}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.TLS = &tls.ConnectionState{} // simulate TLS connection
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if v := rr.Header().Get("strict-transport-security"); v == "" {
		t.Error("HSTS header must be present on HTTPS response")
	}
}

func TestOWASP_RequestBodyLimit(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxRequestBodyBytes: 10}}
	h := mw.OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	body := strings.NewReader(strings.Repeat("x", 100))
	req := httptest.NewRequest("POST", "/", body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rr.Code)
	}
}
