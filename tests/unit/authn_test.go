//go:build !no_authn

package unit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

func TestAuthnJWT_AllowsTrustedSub(t *testing.T) {
	// >= 112 bits required under GODEBUG=fips140=only.
	const hmac_key = "0123456789abcdef" // 16 bytes

	cfg := config.Config{
		Authn: config.AuthnConfig{
			TrustedIDs:     []string{"alice"},
			TrustedSigners: []string{hmac_key},
			Enabled:        true,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString([]byte(hmac_key))
	if err != nil {
		t.Fatal(err)
	}

	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}), logging.New(logging.Config{JSON: false}))

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthnJWT_MissingAuth(t *testing.T) {
	const key = "0123456789abcdef"
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{key}, Enabled: true}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthnJWT_ExpiredToken(t *testing.T) {
	const key = "0123456789abcdef"
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{key}, Enabled: true}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(-time.Minute).Unix(), // already expired
	})
	raw, err := token.SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthnJWT_WrongSigner(t *testing.T) {
	const trustedKey = "trusted-key-1234" // 16 bytes
	const wrongKey = "wrong-key-16byte!"  // 17 bytes — different key
	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{trustedKey}, Enabled: true}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString([]byte(wrongKey))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthnJWT_ForbiddenSub(t *testing.T) {
	const key = "0123456789abcdef"
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedIDs:     []string{"alice"},
		TrustedSigners: []string{key},
		Enabled:        true,
	}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "bob", // not in TrustedIDs
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestAuthnJWT_RS256Accepted(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{pubPEM}, Enabled: true}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString(privKey)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuthnJWT_ES256Accepted(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	cfg := config.Config{Authn: config.AuthnConfig{TrustedSigners: []string{pubPEM}, Enabled: true}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString(privKey)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuthnJWT_SecondSignerAccepted(t *testing.T) {
	const firstKey = "wrong-key-16byte" // 16 bytes — wrong key, tried first
	const secondKey = "0123456789abcdef" // 16 bytes — correct key, tried second

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{firstKey, secondKey},
		Enabled:        true,
	}}
	h := mw.AuthnJWT(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{JSON: false}))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString([]byte(secondKey))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (second signer accepted), got %d", rr.Code)
	}
}
