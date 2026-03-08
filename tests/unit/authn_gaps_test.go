//go:build !no_authn

// tests/unit/authn_gaps_test.go
package unit

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
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func rsaPublicKeyPEM(t *testing.T, priv *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func ecPublicKeyPEM(t *testing.T, priv *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func authnHandler(cfg config.Config) http.Handler {
	return mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{Out: io.Discard}))
}

func bearerReq(t *testing.T, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+token)
	return req
}

func signHS256(t *testing.T, key string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signRS256(t *testing.T, priv *rsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signRS256WithKid(t *testing.T, priv *rsa.PrivateKey, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signES256WithKid(t *testing.T, priv *ecdsa.PrivateKey, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signES256(t *testing.T, priv *ecdsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signES384(t *testing.T, priv *ecdsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES384, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// jwksServerWith serves the given JSON keys array.
func jwksServerWith(t *testing.T, keys []any) *httptest.Server {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
}

func rsaJWK(pub *rsa.PublicKey, kid string) map[string]any {
	e := big.NewInt(int64(pub.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(e),
	}
}

func ecJWK(pub *ecdsa.PublicKey, kid, crv string) map[string]any {
	return map[string]any{
		"kty": "EC",
		"kid": kid,
		"crv": crv,
		"x":   base64.RawURLEncoding.EncodeToString(pub.X.Bytes()),
		"y":   base64.RawURLEncoding.EncodeToString(pub.Y.Bytes()),
	}
}

// ---------------------------------------------------------------------------
// parsePEMPublicKey missing branches
// ---------------------------------------------------------------------------

// No valid PEM block: string starts with "-----BEGIN" but has no footer.
func TestAuthn_PEM_NoPEMBlock(t *testing.T) {
	fakePEM := "-----BEGIN BROKEN DATA\nno actual pem content here"
	const hmacKey = "0123456789abcdef"

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{fakePEM},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signHS256(t, hmacKey)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// RSA public key configured but token uses ES256 — alg mismatch.
func TestAuthn_PEM_RSAKeyWrongAlg(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{rsaPublicKeyPEM(t, rsaPriv)},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, ecPriv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// EC public key configured but token uses HS256 — alg mismatch.
func TestAuthn_PEM_ECKeyWrongAlg(t *testing.T) {
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const hmacKey = "0123456789abcdef"

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{ecPublicKeyPEM(t, ecPriv)},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signHS256(t, hmacKey)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// resolveStaticKey: plain-string signer with non-HMAC algorithm
// ---------------------------------------------------------------------------

func TestAuthn_SecretSignerWithNonHMACAlg(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	// Signer is a plain string (not PEM); token uses RS256.
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{"my-plain-secret"},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, rsaPriv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// JWKS: fetchJWKS, parseJWK, parseRSAJWK, parseECJWK
// ---------------------------------------------------------------------------

// RSA key in JWKS, kid in token — happy path.
func TestJWKS_RSA_AcceptedWithKid(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "k1")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256WithKid(t, priv, "k1")))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// RSA key in JWKS, no kid in token — alg-only matching.
func TestJWKS_RSA_AlgOnlyMatch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// Serve key without kid.
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	// Token has no kid — keysFor falls through to alg matching.
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// EC P-256 key in JWKS — happy path; covers parseECJWK P-256.
func TestJWKS_EC_P256_Accepted(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{ecJWK(&priv.PublicKey, "ec1", "P-256")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256WithKid(t, priv, "ec1")))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// EC P-384 key in JWKS; covers parseECJWK P-384.
func TestJWKS_EC_P384_Accepted(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{ecJWK(&priv.PublicKey, "ec2", "P-384")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES384(t, priv)))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// Cache hit: fetch once, shut server, second request still works.
func TestJWKS_CacheHit(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "k1")})

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	h := authnHandler(cfg)

	// First request: populates cache.
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, bearerReq(t, signRS256WithKid(t, priv, "k1")))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rr1.Code)
	}

	// Shut down the JWKS server; cache must serve the second request.
	srv.Close()

	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, bearerReq(t, signRS256WithKid(t, priv, "k1")))
	if rr2.Code != http.StatusOK {
		t.Errorf("second request (cache hit): expected 200, got %d", rr2.Code)
	}
}

// Unsupported kty in JWKS: key is skipped → no match → 401.
func TestJWKS_UnsupportedKty_Skipped(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{
		map[string]any{"kty": "oct", "k": "secret"}, // unsupported
	})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in RSA JWK n field: key skipped → 401.
func TestJWKS_RSA_BadBase64N(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "RSA",
		"kid": "bad",
		"n":   "!!!not-valid-base64url",
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in RSA JWK e field: key skipped → 401.
func TestJWKS_RSA_BadBase64E(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "RSA",
		"kid": "bad",
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   "!!!not-valid-base64url",
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Unsupported EC curve in JWKS: key skipped → 401.
func TestJWKS_EC_UnsupportedCurve(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "EC",
		"kid": "bad",
		"crv": "P-521", // unsupported
		"x":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.X.Bytes()),
		"y":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.Y.Bytes()),
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	ecPriv, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES512, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(ecPriv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, raw))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in EC JWK x field: key skipped → 401.
func TestJWKS_EC_BadBase64X(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "EC",
		"kid": "bad",
		"crv": "P-256",
		"x":   "!!!not-valid-base64url",
		"y":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.Y.Bytes()),
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in EC JWK y field: key skipped → 401.
func TestJWKS_EC_BadBase64Y(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "EC",
		"kid": "bad",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.X.Bytes()),
		"y":   "!!!not-valid-base64url",
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Kid mismatch: token kid doesn't match any JWK kid → no match → 401.
func TestJWKS_KidMismatch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "server-kid")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	// Token has kid="wrong" which won't match "server-kid".
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256WithKid(t, priv, "wrong")))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// RSA JWKS with ES256 token: jwkAlgMatches(rsa, "ES256") = false → no match → 401.
func TestJWKS_AlgMismatch_RSAKeyECToken(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&rsaPriv.PublicKey, "")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	// ES256 token: jwkAlgMatches(*rsa.PublicKey, "ES256") → false
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, ecPriv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// EC JWKS with RS256 token: jwkAlgMatches(ec, "RS256") = false → no match → 401.
func TestJWKS_AlgMismatch_ECKeyRSAToken(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{ecJWK(&ecPriv.PublicKey, "", "P-256")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	// RS256 token: jwkAlgMatches(*ecdsa.PublicKey, "RS256") → false
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, rsaPriv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// JWKS endpoint returns invalid JSON: fetchJWKS decode error → 401.
func TestJWKS_FetchError_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// JWKS endpoint unreachable: HTTP fetch fails → 401.
func TestJWKS_FetchError_ServerDown(t *testing.T) {
	srv := jwksServerWith(t, []any{})
	url := srv.URL
	srv.Close() // shut down immediately; subsequent fetches fail

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{url},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseJWK json.Unmarshal error: element is a JSON number, not an object.
func TestJWKS_ParseJWK_UnmarshalError(t *testing.T) {
	// Serve {"keys": [42]} — each key element must be an object; a number causes
	// json.Unmarshal into the base struct to fail.
	payload := []byte(`{"keys": [42]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseRSAJWK json.Unmarshal error: n field is a number, not a string.
func TestJWKS_ParseRSAJWK_UnmarshalError(t *testing.T) {
	// Valid kty="RSA" for base decode, but n is a JSON number → error in parseRSAJWK.
	payload := []byte(`{"keys": [{"kty":"RSA","kid":"x","n":123,"e":"AQAB"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseECJWK json.Unmarshal error: x field is a number, not a string.
func TestJWKS_ParseECJWK_UnmarshalError(t *testing.T) {
	payload := []byte(`{"keys": [{"kty":"EC","kid":"x","crv":"P-256","x":123,"y":"dGVzdA"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseWithAllSigners: empty signers list → "no trusted signers configured".
func TestAuthn_EmptySigners(t *testing.T) {
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: nil, // empty
		Enabled:        true,
	}}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parsePEMPublicKey: valid PEM block but invalid PKIX DER bytes → ParsePKIXPublicKey error.
func TestAuthn_PEM_InvalidDER(t *testing.T) {
	badPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: []byte("not valid DER bytes"),
	}))

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{badPEM},
		Enabled:        true,
	}}
	const hmacKey = "0123456789abcdef"
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signHS256(t, hmacKey)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parsePEMPublicKey: unsupported public key type (Ed25519) → default case.
func TestAuthn_PEM_UnsupportedKeyType(t *testing.T) {
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(edPub)
	if err != nil {
		t.Fatal(err)
	}
	edPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{edPEM},
		Enabled:        true,
	}}
	// Any token alg triggers resolveStaticKey → parsePEMPublicKey → default case.
	const hmacKey = "0123456789abcdef"
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signHS256(t, hmacKey)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}
