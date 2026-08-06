//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// hs256Token mints an HS256 JWT with the given secret and subject claim.
func hs256Token(t *testing.T, secret, sub string) string {
	t.Helper()
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("HS256 short-key fixtures are invalid under FIPS 140-only mode (HMAC keys must be >= 112 bits)")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": sub})
	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func rsaPubPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func ecPubPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// ---------------------------------------------------------------------------
// AuthnJWT handler
// ---------------------------------------------------------------------------

func TestAuthnJWT_MissingBearer_401(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	h := AuthnJWT(config.Config{}, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing bearer, got %d", rr.Code)
	}
}

func TestAuthnJWT_InvalidToken_401(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{"secret-key"}}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer not.a.valid.token")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token, got %d", rr.Code)
	}
}

func TestAuthnJWT_ValidToken_NoAllowList_200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{"secret-key"}}}
	called := false
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !called {
		t.Errorf("expected 200 and inner called, got %d called=%v", rr.Code, called)
	}
}

func TestAuthnJWT_ValidToken_AllowedSub_200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{"secret-key"},
		TrustedIDs:     []string{"alice"},
	}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for allowed sub, got %d", rr.Code)
	}
}

func TestAuthnJWT_ValidToken_ForbiddenSub_403(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{"secret-key"},
		TrustedIDs:     []string{"bob"},
	}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for forbidden sub, got %d", rr.Code)
	}
}

func TestAuthnJWT_GetSignersDynamic_200(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	getSigners := func() []string { return []string{"secret-key"} }
	h := AuthnJWT(config.Config{}, getSigners, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("authorization", "Bearer "+hs256Token(t, "secret-key", "alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 via dynamic getSigners, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// parseWithAllSigners — success after an earlier signer fails
// ---------------------------------------------------------------------------

func TestParseWithAllSigners_SecondSignerSucceeds(t *testing.T) {
	cache := newJWKSCache()
	raw := hs256Token(t, "right-secret", "alice")
	claims, err := parseWithAllSigners(raw, []string{"wrong-secret", "right-secret"}, cache)
	if err != nil {
		t.Fatalf("expected success on second signer, got %v", err)
	}
	if claims["sub"] != "alice" {
		t.Errorf("expected sub=alice, got %v", claims["sub"])
	}
}

// ---------------------------------------------------------------------------
// signerKeyfunc — http signer routes to the JWKS cache
// ---------------------------------------------------------------------------

func TestSignerKeyfunc_HTTPSigner_UsesCache(t *testing.T) {
	cache := newJWKSCache()
	kf := signerKeyfunc("http://127.0.0.1:1/jwks.json", cache)
	tok := &jwt.Token{Method: jwt.SigningMethodRS256, Header: map[string]any{}}
	if _, err := kf(tok); err == nil {
		t.Error("expected error from unreachable JWKS endpoint via signerKeyfunc")
	}
}

func TestSignerKeyfunc_StaticSigner_ResolvesKey(t *testing.T) {
	cache := newJWKSCache()
	kf := signerKeyfunc("my-secret", cache)
	tok := &jwt.Token{Method: jwt.SigningMethodHS256, Header: map[string]any{}}
	key, err := kf(tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(key.([]byte)) != "my-secret" {
		t.Errorf("expected HMAC secret bytes, got %v", key)
	}
}

// ---------------------------------------------------------------------------
// resolveStaticKey — PEM branch
// ---------------------------------------------------------------------------

func TestResolveStaticKey_PEM_RSA(t *testing.T) {
	key, err := resolveStaticKey(rsaPubPEM(t), "RS256")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Errorf("expected *rsa.PublicKey, got %T", key)
	}
}

// ---------------------------------------------------------------------------
// parsePEMPublicKey — all branches
// ---------------------------------------------------------------------------

func TestParsePEMPublicKey_NoBlock(t *testing.T) {
	if _, err := parsePEMPublicKey("not a pem", "RS256"); err == nil {
		t.Error("expected error when no PEM block present")
	}
}

func TestParsePEMPublicKey_ParseError(t *testing.T) {
	bad := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("garbage-der")}))
	if _, err := parsePEMPublicKey(bad, "RS256"); err == nil {
		t.Error("expected error parsing garbage DER")
	}
}

func TestParsePEMPublicKey_RSA_Good(t *testing.T) {
	if _, err := parsePEMPublicKey(rsaPubPEM(t), "RS384"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePEMPublicKey_RSA_WrongAlg(t *testing.T) {
	if _, err := parsePEMPublicKey(rsaPubPEM(t), "ES256"); err == nil {
		t.Error("expected error: RSA key cannot verify ES256")
	}
}

func TestParsePEMPublicKey_EC_Good(t *testing.T) {
	if _, err := parsePEMPublicKey(ecPubPEM(t), "ES256"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePEMPublicKey_EC_WrongAlg(t *testing.T) {
	if _, err := parsePEMPublicKey(ecPubPEM(t), "RS256"); err == nil {
		t.Error("expected error: EC key cannot verify RS256")
	}
}

func TestParsePEMPublicKey_UnsupportedType(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	if _, err := parsePEMPublicKey(pemStr, "EdDSA"); err == nil {
		t.Error("expected error for unsupported ed25519 public key type")
	}
}

// ---------------------------------------------------------------------------
// jwksCache.keysFor
// ---------------------------------------------------------------------------

func ecJWKSServer(t *testing.T) *httptest.Server {
	t.Helper()
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	body := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"k1"}]}`, x, y)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func TestKeysFor_GetError(t *testing.T) {
	cache := newJWKSCache()
	tok := &jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{}}
	if _, err := cache.keysFor("http://127.0.0.1:1/jwks.json", tok); err == nil {
		t.Error("expected error from unreachable endpoint")
	}
}

func TestKeysFor_KidAndAlgMatch(t *testing.T) {
	srv := ecJWKSServer(t)
	defer srv.Close()
	cache := newJWKSCache()
	tok := &jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{"kid": "k1"}}
	key, err := cache.keysFor(srv.URL, tok)
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	if key == nil {
		t.Error("expected non-nil key")
	}
}

func TestKeysFor_KidMismatch_NotFound(t *testing.T) {
	srv := ecJWKSServer(t)
	defer srv.Close()
	cache := newJWKSCache()
	tok := &jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{"kid": "does-not-exist"}}
	if _, err := cache.keysFor(srv.URL, tok); err == nil {
		t.Error("expected not-found error for mismatched kid")
	}
}

func TestKeysFor_AlgMismatch_NotFound(t *testing.T) {
	srv := ecJWKSServer(t)
	defer srv.Close()
	cache := newJWKSCache()
	// No kid → matched by alg family only; RS256 does not match an EC key.
	tok := &jwt.Token{Method: jwt.SigningMethodRS256, Header: map[string]any{}}
	if _, err := cache.keysFor(srv.URL, tok); err == nil {
		t.Error("expected not-found error for alg mismatch")
	}
}

// ---------------------------------------------------------------------------
// fetchJWKS — decode error
// ---------------------------------------------------------------------------

func TestFetchJWKS_BadJSON_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "not json at all")
	}))
	defer srv.Close()
	if _, err := fetchJWKS(srv.URL); err == nil {
		t.Error("expected decode error for non-JSON body")
	}
}

// ---------------------------------------------------------------------------
// parseRSAJWK / parseECJWK — error branches (routed through parseJWK)
// ---------------------------------------------------------------------------

func TestParseJWK_RSA_UnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"kty":"RSA","n":123,"e":"AQAB"}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected unmarshal error for numeric n")
	}
}

func TestParseJWK_RSA_BadN(t *testing.T) {
	raw := json.RawMessage(`{"kty":"RSA","n":"!!!bad","e":"AQAB"}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad n")
	}
}

func TestParseJWK_RSA_BadE(t *testing.T) {
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"!!!bad"}`, n))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad e")
	}
}

func TestParseJWK_EC_UnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"kty":"EC","crv":123}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected unmarshal error for numeric crv")
	}
}

func TestParseJWK_EC_BadX(t *testing.T) {
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":"!!!bad","y":%q}`, y))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad x")
	}
}

func TestParseJWK_EC_BadY(t *testing.T) {
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":"!!!bad"}`, x))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad y")
	}
}

// ---------------------------------------------------------------------------
// NewJWTSigner — read-error and success paths
// ---------------------------------------------------------------------------

func TestNewJWTSigner_ReadError(t *testing.T) {
	if _, err := NewJWTSigner("svc", "/nonexistent-keel-key-file.pem"); err == nil {
		t.Error("expected error reading nonexistent key file")
	}
}

func TestNewJWTSigner_Success(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := NewJWTSigner("svc", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signer == nil {
		t.Error("expected non-nil signer")
	}
}

// parseJWK returns an error when the base envelope fails to unmarshal.
func TestParseJWK_BaseUnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"kty":123}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected error when kty is not a string")
	}
}

// LoadTrustedSigners logs a warning when the scanner errors (line too long).
func TestLoadTrustedSigners_ScannerError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signers.txt")
	// A single line larger than bufio.Scanner's default 64 KiB token limit,
	// with no trailing newline, makes scanner.Scan return false and Err()
	// report bufio.ErrTooLong.
	big := make([]byte, 70*1024)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.AuthnConfig{TrustedSignersFile: path}
	log := logging.New(logging.Config{Out: io.Discard})
	got := LoadTrustedSigners(cfg, log)
	if len(got) != 0 {
		t.Errorf("expected no signers on scanner error, got %v", got)
	}
}
