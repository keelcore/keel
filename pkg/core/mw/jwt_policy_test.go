//go:build !no_authn

package mw

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

const policySecret = "policy-test-hmac-secret-0123456789abcdef"

func policyToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("HMAC fixtures not used under FIPS 140-only mode")
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(policySecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func authnStatus(t *testing.T, cfg config.Config, token string) int {
	t.Helper()
	cfg.Authn.TrustedSigners = []string{policySecret}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logging.New(logging.Config{Out: io.Discard}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// A token without exp would be valid forever; it is rejected.
func TestAuthnJWT_RequiresExp(t *testing.T) {
	if code := authnStatus(t, config.Config{}, policyToken(t, jwt.MapClaims{"sub": "svc"})); code != http.StatusUnauthorized {
		t.Fatalf("token without exp: status %d, want 401", code)
	}
	if code := authnStatus(t, config.Config{}, policyToken(t, jwt.MapClaims{"sub": "svc", "exp": time.Now().Add(time.Minute).Unix()})); code != http.StatusOK {
		t.Fatalf("token with exp: status %d, want 200", code)
	}
}

// When authn.audience is set, aud must name this service: a token minted by
// the same signer for another service (or with no aud) is not replayable here.
func TestAuthnJWT_AudienceEnforced(t *testing.T) {
	var cfg config.Config
	cfg.Authn.Audience = "svc-a"
	exp := time.Now().Add(time.Minute).Unix()
	cases := map[string]struct {
		claims jwt.MapClaims
		want   int
	}{
		"matching aud": {jwt.MapClaims{"sub": "x", "exp": exp, "aud": "svc-a"}, http.StatusOK},
		"other aud":    {jwt.MapClaims{"sub": "x", "exp": exp, "aud": "svc-b"}, http.StatusUnauthorized},
		"missing aud":  {jwt.MapClaims{"sub": "x", "exp": exp}, http.StatusUnauthorized},
	}
	for name, tc := range cases {
		if code := authnStatus(t, cfg, policyToken(t, tc.claims)); code != tc.want {
			t.Errorf("%s: status %d, want %d", name, code, tc.want)
		}
	}
}

// Without authn.audience, aud is not checked (backward compatible).
func TestAuthnJWT_NoAudienceConfiguredAcceptsAnyAud(t *testing.T) {
	tok := policyToken(t, jwt.MapClaims{"sub": "x", "exp": time.Now().Add(time.Minute).Unix(), "aud": "anything"})
	if code := authnStatus(t, config.Config{}, tok); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
}

// Algorithms outside the pinned set are rejected even with a matching key.
func TestAuthnJWT_UnpinnedAlgorithmRejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pub := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	tok, err := jwt.NewWithClaims(jwt.SigningMethodPS256, jwt.MapClaims{"sub": "x", "exp": time.Now().Add(time.Minute).Unix()}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWithAllSigners(tok, []string{pub}, newJWKSCache(), jwtParserOptions(config.AuthnConfig{})...); err == nil {
		t.Fatal("PS256 token accepted; only the pinned algorithms may verify")
	}
}

// The outbound signer stamps aud only when sidecar.upstream_audience is set.
func TestJWTSigner_WithAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signer.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, aud := range []string{"", "upstream-svc"} {
		sg, err := NewJWTSigner("keel-a", path)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if err := sg.WithAudience(aud).SignRequest(req); err != nil {
			t.Fatal(err)
		}
		claims := jwt.MapClaims{}
		raw := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }); err != nil {
			t.Fatal(err)
		}
		got, has := claims["aud"]
		if aud == "" && has {
			t.Errorf("aud set (%v) with no audience configured", got)
		}
		if aud != "" && got != aud {
			t.Errorf("aud = %v, want %q", got, aud)
		}
	}
}

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
