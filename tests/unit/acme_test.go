//go:build !no_acme

package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
)

func TestACME_ChallengeRouteServedWithoutRedirect(t *testing.T) {
	mgr := acme.New()
	mgr.SetToken("abc123", "abc123.keyauth")
	h := mgr.HTTPHandler(443)

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
	h := mgr.HTTPHandler(443)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d", rr.Code)
	}
}

func TestACME_NonChallengePath_RedirectsToHTTPS(t *testing.T) {
	mgr := acme.New()
	h := mgr.HTTPHandler(8443)

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
	h := mgr.HTTPHandler(443)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/tok1", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rr.Code)
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

// HTTPHandler: redirect with httpsPort=443 → host must not include ":443".
func TestACME_Redirect_Port443_NoPortInLocation(t *testing.T) {
	mgr := acme.New()
	h := mgr.HTTPHandler(443)

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
	h := mgr.HTTPHandler(8443)

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

// Validate: always returns nil.
func TestACME_Validate_ReturnsNil(t *testing.T) {
	if err := acme.Validate(config.Config{}); err != nil {
		t.Errorf("Validate: expected nil, got %v", err)
	}
}
