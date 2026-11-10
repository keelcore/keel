//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// parsePrivateKey — ECDSA P-256 (PKCS8)
// ---------------------------------------------------------------------------

func TestParsePrivateKey_ECDSA_P256_PKCS8(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	k, method, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey ECDSA P-256 PKCS8: %v", err)
	}
	if k == nil {
		t.Error("expected non-nil key")
	}
	if method == nil {
		t.Error("expected non-nil signing method")
	}
}

// ---------------------------------------------------------------------------
// parsePrivateKey — ECDSA P-384
// ---------------------------------------------------------------------------

func TestParsePrivateKey_ECDSA_P384_SEC1(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	k, method, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey ECDSA P-384 SEC1: %v", err)
	}
	if k == nil {
		t.Error("expected non-nil key")
	}
	if method == nil {
		t.Error("expected non-nil signing method")
	}
}

// ---------------------------------------------------------------------------
// parsePrivateKey — RSA (PKCS#1)
// ---------------------------------------------------------------------------

func TestParsePrivateKey_RSA_PKCS1(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})

	k, method, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey RSA PKCS#1: %v", err)
	}
	if k == nil {
		t.Error("expected non-nil key")
	}
	if method == nil {
		t.Error("expected non-nil signing method")
	}
}

// ---------------------------------------------------------------------------
// parsePrivateKey — no PEM block
// ---------------------------------------------------------------------------

func TestParsePrivateKey_NoPEMBlock(t *testing.T) {
	_, _, err := parsePrivateKey([]byte("not a pem"))
	if err == nil {
		t.Error("expected error for non-PEM input")
	}
}

// ---------------------------------------------------------------------------
// ecSigningMethod — P-256, P-384, P-521, unsupported
// ---------------------------------------------------------------------------

func TestEcSigningMethod_P256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	m, err := ecSigningMethod(key)
	if err != nil {
		t.Fatalf("P256: %v", err)
	}
	if m.Alg() != "ES256" {
		t.Errorf("expected ES256, got %s", m.Alg())
	}
}

func TestEcSigningMethod_P384(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	m, err := ecSigningMethod(key)
	if err != nil {
		t.Fatalf("P384: %v", err)
	}
	if m.Alg() != "ES384" {
		t.Errorf("expected ES384, got %s", m.Alg())
	}
}

func TestEcSigningMethod_P521(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	m, err := ecSigningMethod(key)
	if err != nil {
		t.Fatalf("P521: %v", err)
	}
	if m.Alg() != "ES512" {
		t.Errorf("expected ES512, got %s", m.Alg())
	}
}

// ---------------------------------------------------------------------------
// jwkAlgMatches — RSA, ECDSA, and unknown key types
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ecSigningMethod — unsupported curve
// ---------------------------------------------------------------------------

func TestEcSigningMethod_UnsupportedCurve(t *testing.T) {
	// Use P-224 — a valid ECDSA curve not supported by JWT ES algorithms.
	key, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ecSigningMethod(key); err == nil {
		t.Error("expected error for unsupported EC curve")
	}
}

// ---------------------------------------------------------------------------
// keyAndMethod — unsupported PKCS8 key type
// ---------------------------------------------------------------------------

func TestKeyAndMethod_UnsupportedPKCS8Type(t *testing.T) {
	// Pass a non-RSA, non-ECDSA value to keyAndMethod directly.
	_, _, err := keyAndMethod("not-a-key")
	if err == nil {
		t.Error("expected error for unsupported PKCS8 key type")
	}
}

// ---------------------------------------------------------------------------
// parsePrivateKey — unsupported PEM key type (garbled DER that isn't any known key)
// ---------------------------------------------------------------------------

func TestParsePrivateKey_UnsupportedKeyType(t *testing.T) {
	// Build a PEM block whose type is not recognised as PKCS8, PKCS1, or SEC1.
	// Use random bytes as DER so all three parse attempts fail.
	garbage := make([]byte, 32)
	for i := range garbage {
		garbage[i] = byte(i)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "UNKNOWN KEY", Bytes: garbage})
	_, _, err := parsePrivateKey(pemData)
	if err == nil {
		t.Error("expected error for unrecognised PEM key type")
	}
}

// ---------------------------------------------------------------------------
// parsePrivateKey — RSA PKCS8
// ---------------------------------------------------------------------------

func TestParsePrivateKey_RSA_PKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	k, method, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey RSA PKCS8: %v", err)
	}
	if k == nil || method == nil {
		t.Error("expected non-nil key and method")
	}
}

// ---------------------------------------------------------------------------
// NewJWTSigner — parse error propagated
// ---------------------------------------------------------------------------

// NewJWTSigner returns an error when the key file contains an unrecognised PEM type.
func TestNewJWTSigner_ParseError_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.pem"

	garbage := pem.EncodeToMemory(&pem.Block{Type: "UNKNOWN KEY", Bytes: []byte("garbage")})
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewJWTSigner("test-service", path); err == nil {
		t.Error("expected error for unrecognised PEM key type in key file")
	}
}

// ---------------------------------------------------------------------------
// SignRequest — round-trip: sign and verify Authorization header present
// ---------------------------------------------------------------------------

// SignRequest returns an error when the key and signing method are mismatched.
func TestSignRequest_MismatchedKeyMethod_ReturnsError(t *testing.T) {
	// RSA key with an ECDSA signing method — SignedString will fail.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	from_jwt, _, err2 := parsePrivateKey(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	}))
	if err2 != nil {
		t.Fatal(err2)
	}
	// Force an incompatible method: ES256 expects an ECDSA key.
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, ecMethod, _ := parsePrivateKey(pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: func() []byte { b, _ := x509.MarshalECPrivateKey(ecKey); return b }(),
	}))
	signer := &JWTSigner{myID: "test", key: from_jwt, method: ecMethod}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err := signer.SignRequest(req); err == nil {
		t.Error("expected error for mismatched key/method in SignRequest")
	}
}

func TestSignRequest_SetsAuthorizationHeader(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	k, method, err := parsePrivateKey(pemData)
	if err != nil {
		t.Fatalf("parsePrivateKey: %v", err)
	}
	signer := &JWTSigner{myID: "test-service", key: k, method: method}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err := signer.SignRequest(req); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	auth := req.Header.Get("Authorization")
	if auth == "" {
		t.Error("expected Authorization header to be set")
	}
	if len(auth) < 8 || auth[:7] != "Bearer " {
		t.Errorf("expected Bearer prefix, got %q", auth)
	}
}

// parsePrivateKey returns an error when a SEC1 EC key uses an unsupported curve.
func TestParsePrivateKey_SEC1_UnsupportedCurve(t *testing.T) {
	// P-224 is a valid EC curve that ecSigningMethod does not support.
	key, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if _, _, err := parsePrivateKey(pemData); err == nil {
		t.Error("expected error for SEC1 EC key with unsupported curve")
	}
}

func FuzzParsePrivateKey(f *testing.F) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"))
	f.Add([]byte("not pem"))
	f.Fuzz(func(t *testing.T, data []byte) {
		key, method, err := parsePrivateKey(data)
		if err == nil && (key == nil || method == nil) {
			t.Fatalf("parsePrivateKey returned no error but key=%v method=%v", key, method)
		}
	})
}

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
