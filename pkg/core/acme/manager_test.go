//go:build !no_acme

package acme_test

// Tests for the ACME http-01 challenge handler.
//
// Spec reference: RFC 8555 §8.3 HTTP Challenge
// https://www.rfc-editor.org/rfc/rfc8555#section-8.3
//
// Key requirements under test:
//   - §8.3: server MUST respond 200 with the key authorization as body
//   - §8.3: Content-Type MUST be "application/octet-stream"
//   - §8.3: unknown / empty tokens MUST return 404
//   - All non-challenge paths MUST redirect to HTTPS (301)

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/acme"
)

func handlerWithToken(httpsPort int) http.Handler {
	m := acme.New()
	m.SetToken("validtoken", "validtoken.thumbprint")
	return m.HTTPHandler(httpsPort)
}

// RFC 8555 §8.3: known token must return 200 with the key authorization as body.
func TestHTTPHandler_KnownToken_200(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/validtoken", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "validtoken.thumbprint" {
		t.Fatalf("want key-auth body %q, got %q", "validtoken.thumbprint", got)
	}
}

// RFC 8555 §8.3: Content-Type MUST be "application/octet-stream".
func TestHTTPHandler_KnownToken_ContentType(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/validtoken", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("want Content-Type application/octet-stream, got %q", ct)
	}
}

// RFC 8555 §8.3: unknown token must return 404.
func TestHTTPHandler_UnknownToken_404(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/nosuchthing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// RFC 8555 §8.3: empty token segment (bare prefix path) must return 404.
func TestHTTPHandler_EmptyToken_404(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// DeleteToken: after removal the token must no longer be served.
func TestHTTPHandler_DeleteToken_404(t *testing.T) {
	m := acme.New()
	m.SetToken("temptoken", "temptoken.thumb")
	m.DeleteToken("temptoken")
	h := m.HTTPHandler(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/temptoken", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 after deletion, got %d", w.Code)
	}
}

// Non-challenge path must redirect to HTTPS (301); standard port 443 → no port in host.
func TestHTTPHandler_Redirect_StandardPort(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/some/path?q=1", nil)
	r.Host = "example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("want 301, got %d", w.Code)
	}
	want := "https://example.com/some/path?q=1"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("want Location %q, got %q", want, got)
	}
}

// Non-challenge path must redirect to HTTPS; non-standard port must appear in host.
func TestHTTPHandler_Redirect_NonStandardPort(t *testing.T) {
	h := handlerWithToken(8443)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("want 301, got %d", w.Code)
	}
	want := "https://example.com:8443/"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("want Location %q, got %q", want, got)
	}
}

// Non-challenge path must redirect even when request host already carries a port.
func TestHTTPHandler_Redirect_HostWithPort(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/foo", nil)
	r.Host = "example.com:80"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("want 301, got %d", w.Code)
	}
	want := "https://example.com/foo"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("want Location %q, got %q", want, got)
	}
}
