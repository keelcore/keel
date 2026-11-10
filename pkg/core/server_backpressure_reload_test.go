package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// authn.trusted_ids is reloadable: wrapMain reads the allowlist from the authn
// snapshot per request, falling back to the startup list before one exists.
func TestWrapMain_TrustedIDsReload(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("HMAC fixtures not used under FIPS 140-only mode")
	}
	const secret = "reload-test-hmac-secret-0123456789abcdef"
	var cfg config.Config
	cfg.Authn.Enabled = true
	cfg.Authn.TrustedSigners = []string{secret}
	cfg.Authn.TrustedIDs = []string{"alice"}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256,
		jwt.MapClaims{"sub": "bob", "exp": time.Now().Add(time.Minute).Unix()}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	s.applyAuthnState(cfg) // startup snapshot: alice only
	if code := call(); code != http.StatusForbidden {
		t.Fatalf("bob before reload: status %d, want 403", code)
	}
	cfg.Authn.TrustedIDs = []string{"bob"}
	s.applyAuthnState(cfg) // reload adds bob
	if code := call(); code != http.StatusOK {
		t.Fatalf("bob after reload: status %d, want 200", code)
	}
}
