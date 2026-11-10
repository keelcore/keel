//go:build !no_owasp

package mw

import (
	"crypto/tls"
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

// OWASP sets HSTS when the connection is TLS and HSTSMaxAge > 0.
func TestOWASP_SetsHSTSOnTLS(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{HSTSMaxAge: 63072000}}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{} // simulate a TLS connection without a handshake
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("strict-transport-security"); got != "max-age=63072000" {
		t.Errorf("expected HSTS max-age=63072000, got %q", got)
	}
}

// OWASP wraps the response writer when MaxResponseBodyBytes > 0.
func TestOWASP_LimitsResponseBody(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 5}}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Body.String() != "hello" {
		t.Errorf("expected truncated body 'hello', got %q", rr.Body.String())
	}
}

// OWASP applies a read timeout context when Timeouts.Read > 0.
func TestOWASP_AppliesReadTimeout(t *testing.T) {
	cfg := config.Config{Timeouts: config.TimeoutsConfig{Read: config.DurationOf(30_000_000_000)}}
	var hadDeadline bool
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if !hadDeadline {
		t.Error("expected request context to carry a deadline when Timeouts.Read > 0")
	}
}
