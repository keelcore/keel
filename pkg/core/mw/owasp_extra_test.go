//go:build !no_owasp

package mw

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

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

// limitedResponseWriter.Unwrap exposes the wrapped writer.
func TestLimitedResponseWriter_Unwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	lw := &limitedResponseWriter{ResponseWriter: rr, remaining: 5}
	if lw.Unwrap() != rr {
		t.Error("expected Unwrap to return the wrapped ResponseWriter")
	}
}
