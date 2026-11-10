//go:build !no_owasp

package mw

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// Normal write: body fits within the limit.
func TestOWASP_ResponseBodyLimit_NormalWrite(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 100}}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if body := rr.Body.String(); body != "hi" {
		t.Errorf("expected %q, got %q", "hi", body)
	}
}

// Truncation: write larger than remaining budget.
func TestOWASP_ResponseBodyLimit_Truncates(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 5}}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world")) // 11 bytes → truncated to 5
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if body := rr.Body.String(); body != "hello" {
		t.Errorf("expected %q, got %q", "hello", body)
	}
}

// Second write when remaining==0: write is silently dropped.
func TestOWASP_ResponseBodyLimit_SecondWriteDropped(t *testing.T) {
	cfg := config.Config{Security: config.SecurityConfig{MaxResponseBodyBytes: 3}}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abc")) // consumes remaining=3 → remaining=0
		_, _ = w.Write([]byte("def")) // remaining==0 → dropped
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if body := rr.Body.String(); body != "abc" {
		t.Errorf("expected %q, got %q", "abc", body)
	}
}

func TestOWASP_ReadTimeout_SetsContext(t *testing.T) {
	cfg := config.Config{
		Timeouts: config.TimeoutsConfig{
			Read: config.DurationOf(time.Second),
		},
	}
	var deadlineSet bool
	h := OWASP(cfg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, deadlineSet = r.Context().Deadline()
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !deadlineSet {
		t.Error("expected context deadline from OWASP read timeout")
	}
}

func TestOWASP_SetsHeaders(t *testing.T) {
	cfg := config.Config{
		Security: config.SecurityConfig{MaxRequestBodyBytes: 1024},
	}
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := OWASP(config.Config{}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := OWASP(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
