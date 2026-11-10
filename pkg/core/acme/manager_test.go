//go:build !no_acme

package acme_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
)

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

func handlerWithToken(httpsPort int) http.Handler {
	m := acme.New()
	m.SetToken("validtoken", "validtoken.thumbprint")
	return m.HTTPHandler(httpsPort, []string{"example.com", "www.example.com"})
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
	h := m.HTTPHandler(443, []string{"example.com"})
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

// S10: the redirect target never comes from a client Host that is not a
// configured domain — a forged Host goes to the primary domain instead.
func TestHTTPHandler_Redirect_ForgedHostGoesToPrimaryDomain(t *testing.T) {
	h := handlerWithToken(443)
	for host, want := range map[string]string{
		"evil.example.net":   "https://example.com/x",
		"203.0.113.7:80":     "https://example.com/x",
		"WWW.Example.COM.":   "https://www.example.com/x",
		"www.example.com:80": "https://www.example.com/x",
	} {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Location"); w.Code != http.StatusMovedPermanently || got != want {
			t.Errorf("Host %q: %d %q, want 301 %q", host, w.Code, got, want)
		}
	}
}

// With no configured domains there is nowhere safe to redirect: 404.
func TestHTTPHandler_Redirect_NoDomains404(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Host = "evil.example.net"
	w := httptest.NewRecorder()
	acme.New().HTTPHandler(443, nil).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestACME_ChallengeRouteServedWithoutRedirect(t *testing.T) {
	mgr := acme.New()
	mgr.SetToken("abc123", "abc123.keyauth")
	h := mgr.HTTPHandler(443, []string{"example.com"})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/abc123", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for challenge, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "abc123.keyauth" {
		t.Errorf("expected key auth body, got %q", body)
	}
}

func TestACME_UnknownToken_Returns404(t *testing.T) {
	mgr := acme.New()
	h := mgr.HTTPHandler(443, []string{"example.com"})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d", rr.Code)
	}
}

func TestACME_NonChallengePath_RedirectsToHTTPS(t *testing.T) {
	mgr := acme.New()
	h := mgr.HTTPHandler(8443, []string{"example.com"})

	req := httptest.NewRequest("GET", "/app/path?q=1", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", rr.Code)
	}
	loc := rr.Header().Get("location")
	if !strings.HasPrefix(loc, "https://") {
		t.Errorf("expected https:// redirect, got %q", loc)
	}
	if !strings.Contains(loc, "8443") {
		t.Errorf("expected port 8443 in redirect, got %q", loc)
	}
	if !strings.Contains(loc, "/app/path?q=1") {
		t.Errorf("expected original path+query in redirect, got %q", loc)
	}
}

func TestACME_DeleteToken_RemovesChallenge(t *testing.T) {
	mgr := acme.New()
	mgr.SetToken("tok1", "tok1.auth")
	mgr.DeleteToken("tok1")
	h := mgr.HTTPHandler(443, []string{"example.com"})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/tok1", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rr.Code)
	}
}

// HTTPHandler: redirect with httpsPort=443 → host must not include ":443".
func TestACME_Redirect_Port443_NoPortInLocation(t *testing.T) {
	mgr := acme.New()
	h := mgr.HTTPHandler(443, []string{"example.com"})

	req := httptest.NewRequest("GET", "/other/path", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rr.Code)
	}
	loc := rr.Header().Get("location")
	if !strings.HasPrefix(loc, "https://example.com/") {
		t.Errorf("expected https://example.com/... redirect, got %q", loc)
	}
	if strings.Contains(loc, ":443") {
		t.Errorf("port 443 must not appear in redirect URL, got %q", loc)
	}
}

// HTTPHandler: host includes a port (SplitHostPort succeeds → h stripped).
func TestACME_Redirect_HostWithPort_StripsSrcPort(t *testing.T) {
	mgr := acme.New()
	h := mgr.HTTPHandler(8443, []string{"example.com"})

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com:80" // source port present → SplitHostPort succeeds
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rr.Code)
	}
	loc := rr.Header().Get("location")
	if !strings.Contains(loc, ":8443") {
		t.Errorf("expected :8443 in redirect, got %q", loc)
	}
	if strings.Contains(loc, ":80") {
		t.Errorf("source port 80 must not appear in redirect, got %q", loc)
	}
}

// Start: returns nil when context is cancelled before any CA contact.
func TestACME_Start_ExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so Start exits without dialling
	mgr := acme.New()
	if err := mgr.Start(ctx, config.ACMEConfig{}); err != nil {
		t.Errorf("Start: expected nil on cancelled context, got %v", err)
	}
}

// Validate: always returns nil.
func TestACME_Validate_ReturnsNil(t *testing.T) {
	if err := acme.Validate(config.Config{}); err != nil {
		t.Errorf("Validate: expected nil, got %v", err)
	}
}
