//go:build !no_acme

package unit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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