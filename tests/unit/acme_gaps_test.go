//go:build !no_acme

// tests/unit/acme_gaps_test.go
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

// Start: stub implementation returns nil.
func TestACME_Start_ReturnsNil(t *testing.T) {
	mgr := acme.New()
	if err := mgr.Start(context.Background(), config.ACMEConfig{}); err != nil {
		t.Errorf("Start: expected nil error, got %v", err)
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
	// Destination port is 8443 (not the source port 80).
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