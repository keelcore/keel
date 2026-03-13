//go:build !no_authn

package mw

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// LoadTrustedSigners
// ---------------------------------------------------------------------------

// LoadTrustedSigners with no file configured returns only the inline list.
func TestLoadTrustedSigners_NoFile(t *testing.T) {
	cfg := config.AuthnConfig{
		TrustedSigners: []string{"signer-a", "signer-b"},
	}
	log := logging.New(logging.Config{Out: io.Discard})
	got := LoadTrustedSigners(cfg, log)
	if len(got) != 2 {
		t.Errorf("expected 2 signers, got %d", len(got))
	}
}

// LoadTrustedSigners with a non-existent file logs a warning and returns inline list.
func TestLoadTrustedSigners_FileNotFound(t *testing.T) {
	cfg := config.AuthnConfig{
		TrustedSigners:     []string{"inline-signer"},
		TrustedSignersFile: "/nonexistent-signers-file-for-test.txt",
	}
	log := logging.New(logging.Config{Out: io.Discard})
	got := LoadTrustedSigners(cfg, log)
	// Should return the inline list unchanged.
	if len(got) != 1 || got[0] != "inline-signer" {
		t.Errorf("expected [inline-signer], got %v", got)
	}
}

// LoadTrustedSigners with a valid file merges inline + file entries.
func TestLoadTrustedSigners_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signers.txt")
	content := "# comment\n\nfile-signer-1\nfile-signer-2\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.AuthnConfig{
		TrustedSigners:     []string{"inline-signer"},
		TrustedSignersFile: path,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	got := LoadTrustedSigners(cfg, log)
	// inline + 2 file signers = 3
	if len(got) != 3 {
		t.Errorf("expected 3 signers (1 inline + 2 file), got %d: %v", len(got), got)
	}
}

// LoadTrustedSigners skips blank lines and comment lines.
func TestLoadTrustedSigners_SkipsBlankAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signers.txt")
	content := "# this is a comment\n\n  \nreal-signer\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.AuthnConfig{
		TrustedSignersFile: path,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	got := LoadTrustedSigners(cfg, log)
	if len(got) != 1 || got[0] != "real-signer" {
		t.Errorf("expected [real-signer], got %v", got)
	}
}

// ---------------------------------------------------------------------------
// parseWithAllSigners — empty signers
// ---------------------------------------------------------------------------

// parseWithAllSigners with empty signers returns an error.
func TestParseWithAllSigners_EmptySigners(t *testing.T) {
	cache := newJWKSCache()
	_, err := parseWithAllSigners("any.token.here", nil, cache)
	if err == nil {
		t.Error("expected error for empty signers list")
	}
}

// ---------------------------------------------------------------------------
// resolveStaticKey — HMAC for non-HS algorithm returns error
// ---------------------------------------------------------------------------

func TestResolveStaticKey_HMACForNonHS_ReturnsError(t *testing.T) {
	_, err := resolveStaticKey("mysecret", "RS256")
	if err == nil {
		t.Error("expected error when HMAC secret used with RS256")
	}
}

// resolveStaticKey returns []byte for HS256.
func TestResolveStaticKey_HMAC_HS256_ReturnsBytes(t *testing.T) {
	key, err := resolveStaticKey("mysecretkey", "HS256")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, ok := key.([]byte)
	if !ok {
		t.Errorf("expected []byte key, got %T", key)
	}
	if string(b) != "mysecretkey" {
		t.Errorf("expected 'mysecretkey', got %q", string(b))
	}
}

// ---------------------------------------------------------------------------
// JWKS — fetchJWKS and parseJWK
// ---------------------------------------------------------------------------

// fetchJWKS returns an error when the endpoint is unreachable.
func TestFetchJWKS_UnreachableEndpoint_ReturnsError(t *testing.T) {
	_, err := fetchJWKS("http://127.0.0.1:1/.well-known/jwks.json")
	if err == nil {
		t.Error("expected error for unreachable JWKS endpoint")
	}
}

// fetchJWKS parses a minimal valid JWK Set with one EC key.
func TestFetchJWKS_ValidECKey(t *testing.T) {
	// Build a minimal P-256 JWK JSON.
	// x and y are base64url-encoded 32-byte zeros (not a valid key, but parses).
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	body := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"k1"}]}`, x, y)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	keys, err := fetchJWKS(srv.URL)
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}
}

// fetchJWKS skips keys with unsupported kty.
func TestFetchJWKS_SkipsUnsupportedKty(t *testing.T) {
	body := `{"keys":[{"kty":"oct","k":"somevalue","kid":"k1"}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	keys, err := fetchJWKS(srv.URL)
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys (skipped oct type), got %d", len(keys))
	}
}

// parseJWK with unsupported kty returns an error.
func TestParseJWK_UnsupportedKty(t *testing.T) {
	raw := json.RawMessage(`{"kty":"oct","kid":"k1"}`)
	_, err := parseJWK(raw)
	if err == nil {
		t.Error("expected error for unsupported kty=oct")
	}
}

// parseJWK for EC with unsupported curve returns an error.
func TestParseJWK_EC_UnsupportedCurve(t *testing.T) {
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	rawStr := fmt.Sprintf(`{"kty":"EC","crv":"P-521","x":%q,"y":%q}`, x, y)
	raw := json.RawMessage(rawStr)
	_, err := parseJWK(raw)
	if err == nil {
		t.Error("expected error for unsupported curve P-521")
	}
}

// jwksCache.get returns an error when the endpoint is unreachable.
func TestJWKSCache_Get_UnreachableEndpoint(t *testing.T) {
	cache := newJWKSCache()
	_, err := cache.get("http://127.0.0.1:1/.well-known/jwks.json")
	if err == nil {
		t.Error("expected error from cache.get for unreachable endpoint")
	}
}

// jwksCache.get returns the cached entry on the second call (TTL not expired).
func TestJWKSCache_Get_CacheHit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		body := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"k1"}]}`, x, y)
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	cache := newJWKSCache()
	e1, err := cache.get(srv.URL)
	if err != nil {
		t.Fatalf("first cache.get: %v", err)
	}

	// Second call must hit cache (no new HTTP request).
	e2, err := cache.get(srv.URL)
	if err != nil {
		t.Fatalf("second cache.get: %v", err)
	}
	if e1 != e2 {
		t.Error("expected same cache entry on second call (cache hit)")
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d (cache not used)", calls)
	}
}

// parseJWK for RSA returns a non-nil key.
func TestParseJWK_RSA_Valid(t *testing.T) {
	// Minimal RSA JWK: n=4 bytes, e=3 bytes (exponent 65537).
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x00, 0x01})
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	rawStr := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":%q,"kid":"rsa1"}`, n, e)
	raw := json.RawMessage(rawStr)
	k, err := parseJWK(raw)
	if err != nil {
		t.Fatalf("parseJWK RSA: %v", err)
	}
	if k.key == nil {
		t.Error("expected non-nil RSA key")
	}
}

// fetchJWKS parses an RSA JWK.
func TestFetchJWKS_ValidRSAKey(t *testing.T) {
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x00, 0x01})
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	body := fmt.Sprintf(`{"keys":[{"kty":"RSA","n":%q,"e":%q,"kid":"rsa1"}]}`, n, e)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	keys, err := fetchJWKS(srv.URL)
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 RSA key, got %d", len(keys))
	}
}

// parseECJWK with P-384 curve returns a key.
func TestParseJWK_EC_P384_Valid(t *testing.T) {
	// P-384 requires 48-byte coordinates.
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 48))
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 48))
	rawStr := fmt.Sprintf(`{"kty":"EC","crv":"P-384","x":%q,"y":%q}`, x, y)
	raw := json.RawMessage(rawStr)
	k, err := parseJWK(raw)
	if err != nil {
		t.Fatalf("parseJWK EC P-384: %v", err)
	}
	if k.key == nil {
		t.Error("expected non-nil EC P-384 key")
	}
}
