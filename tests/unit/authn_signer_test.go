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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeRSAKeyFile(t *testing.T) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "rsa-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name(), &key.PublicKey
}

func writeECKeyFile(t *testing.T, curve elliptic.Curve) (string, *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "ec-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name(), &key.PublicKey
}

// ---------------------------------------------------------------------------
// NewJWTSigner
// ---------------------------------------------------------------------------

func TestJWTSigner_RSA_SignsAndVerifies(t *testing.T) {
	keyFile, pub := writeRSAKeyFile(t)
	signer, err := mw.NewJWTSigner("svc-a", keyFile)
	if err != nil {
		t.Fatalf("NewJWTSigner: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	if err := signer.SignRequest(req); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("expected Bearer prefix, got: %s", auth)
	}
	raw := strings.TrimPrefix(auth, "Bearer ")

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return pub, nil
	})
	if err != nil {
		t.Fatalf("verify RSA JWT: %v", err)
	}
	if claims["sub"] != "svc-a" || claims["iss"] != "svc-a" {
		t.Errorf("unexpected claims: %v", claims)
	}
}

func TestJWTSigner_ECDSA_P256_SignsAndVerifies(t *testing.T) {
	keyFile, pub := writeECKeyFile(t, elliptic.P256())
	signer, err := mw.NewJWTSigner("svc-b", keyFile)
	if err != nil {
		t.Fatalf("NewJWTSigner: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	if err := signer.SignRequest(req); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	auth := req.Header.Get("Authorization")
	raw := strings.TrimPrefix(auth, "Bearer ")

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return pub, nil
	})
	if err != nil {
		t.Fatalf("verify EC JWT: %v", err)
	}
}

func TestJWTSigner_BadKeyFile_ReturnsError(t *testing.T) {
	_, err := mw.NewJWTSigner("x", "/nonexistent/key.pem")
	if err == nil {
		t.Error("expected error for missing key file, got nil")
	}
}

func TestJWTSigner_SignRequest_ClaimsHaveTTL(t *testing.T) {
	keyFile, _ := writeRSAKeyFile(t)
	signer, err := mw.NewJWTSigner("svc-c", keyFile)
	if err != nil {
		t.Fatal(err)
	}

	before := time.Now().Unix()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	_ = signer.SignRequest(req)

	raw := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	token, _, err := new(jwt.Parser).ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	claims := token.Claims.(jwt.MapClaims)

	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatal("exp claim missing or wrong type")
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		t.Fatal("iat claim missing or wrong type")
	}
	if int64(iat) < before {
		t.Errorf("iat %v before request start %v", iat, before)
	}
	ttl := int64(exp) - int64(iat)
	if ttl < 290 || ttl > 310 {
		t.Errorf("expected ~300s TTL, got %ds", ttl)
	}
}

// ---------------------------------------------------------------------------
// loadTrustedSigners (via AuthnJWT integration)
// ---------------------------------------------------------------------------

func writeTrustedSignersFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "signers-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, _ = f.WriteString(content)
	f.Close()
	return f.Name()
}

func TestAuthn_TrustedSignersFile_MergesWithInline(t *testing.T) {
	const key1 = "0123456789abcdef"
	const key2 = "fedcba9876543210"

	sigFile := writeTrustedSignersFile(t, key2+"\n")

	cfg := config.Config{
		Authn: config.AuthnConfig{
			Enabled:            true,
			TrustedSigners:     []string{key1},
			TrustedSignersFile: sigFile,
		},
	}
	log := logging.New(logging.Config{Out: &strings.Builder{}})

	// Token signed with key2 (from file) should be accepted.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "any",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := token.SignedString([]byte(key2))
	if err != nil {
		t.Fatal(err)
	}

	var reached bool
	h := mw.AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !reached {
		t.Errorf("handler not reached; status=%d", rr.Code)
	}
}

func TestAuthn_TrustedSignersFile_MissingFile_WarnsAndContinues(t *testing.T) {
	const key = "0123456789abcdef"

	var logBuf strings.Builder
	log := logging.New(logging.Config{JSON: true, Out: &logBuf})

	cfg := config.Config{
		Authn: config.AuthnConfig{
			Enabled:            true,
			TrustedSigners:     []string{key},
			TrustedSignersFile: "/nonexistent/signers.txt",
		},
	}

	// Inline signer still works despite missing file.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "any",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, _ := token.SignedString([]byte(key))

	var reached bool
	h := mw.AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Error("inline signer should still work when file is missing")
	}
	if !strings.Contains(logBuf.String(), "trusted_signers_file_open_failed") {
		t.Error("expected warning about missing signers file")
	}
}

func TestAuthn_TrustedSignersFile_CommentsAndBlankLines(t *testing.T) {
	const key = "0123456789abcdef"

	content := "# this is a comment\n\n" + key + "\n\n# another comment\n"
	sigFile := writeTrustedSignersFile(t, content)

	cfg := config.Config{
		Authn: config.AuthnConfig{
			Enabled:            true,
			TrustedSignersFile: sigFile,
		},
	}
	log := logging.New(logging.Config{Out: &strings.Builder{}})

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "any",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, _ := token.SignedString([]byte(key))

	var reached bool
	h := mw.AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Error("key from file should be accepted when comments and blank lines are present")
	}
}
