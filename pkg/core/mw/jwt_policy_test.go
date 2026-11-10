//go:build !no_authn

package mw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// With WithTrustedIDs the allowlist is read per request, so a reload that
// removes a principal revokes it immediately.
func TestAuthnJWT_WithTrustedIDsIsLive(t *testing.T) {
	set := TrustedIDSet([]string{"alice"})
	var cfg config.Config
	cfg.Authn.TrustedSigners = []string{policySecret}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		logging.New(logging.Config{Out: io.Discard}), WithTrustedIDs(func() map[string]struct{} { return set }))
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+policyToken(t, jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(time.Minute).Unix()}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call(); code != http.StatusOK {
		t.Fatalf("allowed principal: status %d, want 200", code)
	}
	set = TrustedIDSet([]string{"bob"}) // reload removes alice
	if code := call(); code != http.StatusForbidden {
		t.Fatalf("revoked principal: status %d, want 403", code)
	}
}
