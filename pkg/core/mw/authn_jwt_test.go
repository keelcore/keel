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
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
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

// p256JWKCoords returns the base64url JWK x and y of a generated P-256 key: the 32-byte
// coordinates of its uncompressed SEC 1 encoding (0x04 || X || Y).
func p256JWKCoords(t testing.TB) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(point[1:33]), base64.RawURLEncoding.EncodeToString(point[33:])
}

func FuzzParseWithAllSigners(f *testing.F) {
	ecPEM := fuzzECPublicPEM(f)
	f.Add("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ4In0.signature", "0123456789abcdef")
	f.Add("eyJhbGciOiJFUzI1NiJ9.e30.", ecPEM)
	f.Add("eyJhbGciOiJub25lIn0.e30.", "secret")
	f.Add("..", "-----BEGIN PUBLIC KEY-----")
	f.Fuzz(func(t *testing.T, token, signer string) {
		claims, err := parseWithAllSigners(token, []string{signer}, nil)
		if err == nil && claims == nil {
			t.Fatalf("parseWithAllSigners accepted %q with nil claims", token)
		}
	})
}

func FuzzResolveStaticKey(f *testing.F) {
	f.Add(fuzzECPublicPEM(f), "ES256")
	f.Add("secret", "HS256")
	f.Add("secret", "RS256")
	f.Add("-----END PUBLIC KEY-----", "HS256")
	f.Fuzz(func(t *testing.T, signer, alg string) {
		key, err := resolveStaticKey(signer, alg)
		if err == nil && key == nil {
			t.Fatalf("resolveStaticKey(%q, %q) returned no error and no key", signer, alg)
		}
	})
}

// fuzzECPublicPEM returns a PEM-encoded P-256 public key for seed inputs.
func fuzzECPublicPEM(f *testing.F) string {
	f.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		f.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// hs256Token mints an HS256 JWT with the given secret and subject claim.
func hs256Token(t *testing.T, secret, sub string) string {
	t.Helper()
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("HS256 short-key fixtures are invalid under FIPS 140-only mode (HMAC keys must be >= 112 bits)")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": sub, "exp": time.Now().Add(time.Hour).Unix()})
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

func TestResolveStaticKey_PEM_RSA(t *testing.T) {
	key, err := resolveStaticKey(rsaPubPEM(t), "RS256")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Errorf("expected *rsa.PublicKey, got %T", key)
	}
}

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

// writeSignersFile writes content to a temp signers file and returns its path.
func writeSignersFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "signers.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadFromFile(t *testing.T, content string) []string {
	t.Helper()
	cfg := config.AuthnConfig{TrustedSignersFile: writeSignersFile(t, content)}
	return LoadTrustedSigners(cfg, logging.New(logging.Config{Out: io.Discard}))
}

// A multi-line PEM public key in the signers file becomes exactly ONE signer
// (the whole block), alongside one-per-line HMAC secrets.
func TestLoadTrustedSigners_PEMBlockIsOneSigner(t *testing.T) {
	pemKey := rsaPubPEM(t)
	got := loadFromFile(t, "hmac-secret-1\n"+pemKey+"\n\nhmac-secret-2\n")
	if len(got) != 3 {
		t.Fatalf("expected 3 signers (2 HMAC + 1 PEM), got %d: %q", len(got), got)
	}
	if got[0] != "hmac-secret-1" || got[2] != "hmac-secret-2" {
		t.Errorf("HMAC secrets not preserved in order: %q", got)
	}
	if _, err := parsePEMPublicKey(got[1], "RS256"); err != nil {
		t.Errorf("PEM signer did not parse as a public key: %v", err)
	}
}

// Blank lines inside a PEM block are tolerated and do not split the block.
func TestLoadTrustedSigners_PEMBlockWithBlankLine(t *testing.T) {
	pemKey := rsaPubPEM(t)
	lines := strings.Split(strings.TrimSpace(pemKey), "\n")
	withBlank := lines[0] + "\n\n" + strings.Join(lines[1:], "\n") + "\n"
	got := loadFromFile(t, withBlank)
	if len(got) != 1 {
		t.Fatalf("expected 1 PEM signer, got %d: %q", len(got), got)
	}
	if _, err := parsePEMPublicKey(got[0], "RS256"); err != nil {
		t.Errorf("PEM signer did not parse: %v", err)
	}
}

// PEM armor outside a block is dropped, never accepted as an HMAC secret.
func TestLoadTrustedSigners_StrayArmorDropped(t *testing.T) {
	got := loadFromFile(t, "-----END PUBLIC KEY-----\nhmac-secret\n")
	if len(got) != 1 || got[0] != "hmac-secret" {
		t.Errorf("expected only the HMAC secret, got %q", got)
	}
}

// An unterminated PEM block is dropped (and its lines never become secrets).
func TestLoadTrustedSigners_UnterminatedPEMDropped(t *testing.T) {
	got := loadFromFile(t, "hmac-secret\n-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEF\n")
	if len(got) != 1 || got[0] != "hmac-secret" {
		t.Errorf("expected only the HMAC secret, got %q", got)
	}
}

// Regression for the forgery: with a PEM key in the signers file, a token
// HMAC-signed using any line of that PEM (here the END armor, which an attacker
// can guess) must be rejected.
func TestAuthnJWT_PEMArmorIsNotAnHMACSecret(t *testing.T) {
	cfg := config.Config{}
	cfg.Authn.TrustedSignersFile = writeSignersFile(t, rsaPubPEM(t))
	log := logging.New(logging.Config{Out: io.Discard})
	signers := LoadTrustedSigners(cfg.Authn, log)
	for _, armor := range []string{"-----END PUBLIC KEY-----", "-----BEGIN PUBLIC KEY-----"} {
		forged := hs256Token(t, armor, "attacker")
		if _, err := parseWithAllSigners(forged, signers, newJWKSCache()); err == nil {
			t.Errorf("token HMAC-signed with %q was accepted", armor)
		}
	}
}

// An inline signer that is a PEM fragment is never usable as an HMAC secret.
func TestResolveStaticKey_ArmorFragmentNotHMAC(t *testing.T) {
	if _, err := resolveStaticKey("-----END PUBLIC KEY-----", "HS256"); err == nil {
		t.Error("expected error: PEM armor must not resolve to an HMAC key")
	}
}

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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}), log)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Error("key from file should be accepted when comments and blank lines are present")
	}
}

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

	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	const firstKey = "wrong-key-16byte"  // 16 bytes — wrong key, tried first
	const secondKey = "0123456789abcdef" // 16 bytes — correct key, tried second

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{firstKey, secondKey},
		Enabled:        true,
	}}
	h := AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestAuthnJWT_AuditsEveryOutcome(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, tc := range []struct {
		name    string
		trusted []string
		token   func(t *testing.T) string
		want    audit.Event
	}{
		{"no token", nil, func(*testing.T) string { return "" },
			audit.Event{Event: "authn_failure", SourceIP: "203.0.113.7", Outcome: "deny", Detail: "missing bearer token"}},
		{"bad token", nil, func(*testing.T) string { return "garbage" },
			audit.Event{Event: "authn_failure", SourceIP: "203.0.113.7", Outcome: "deny", Detail: "invalid token"}},
		{"valid", nil, func(t *testing.T) string { return hs256Token(t, auditTestSecret, "alice") },
			audit.Event{Event: "authn_success", Principal: "alice", SourceIP: "203.0.113.7", Outcome: "allow"}},
		{"not on the allow list", []string{"bob"}, func(t *testing.T) string { return hs256Token(t, auditTestSecret, "alice") },
			audit.Event{Event: "authn_forbidden", Principal: "alice", SourceIP: "203.0.113.7", Outcome: "deny"}},
	} {
		h, got := auditAuthn(t, tc.trusted, ok)
		h.ServeHTTP(httptest.NewRecorder(), auditRequest(tc.token(t)))
		if len(*got) != 1 || (*got)[0] != tc.want {
			t.Errorf("%s: events = %+v, want [%+v]", tc.name, *got, tc.want)
		}
	}
}

// The authenticated subject reaches later middleware through the request context.
func TestAuthnJWT_PutsThePrincipalInTheContext(t *testing.T) {
	var principal any
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { principal = r.Context().Value(ctxkeys.Principal) })
	h, _ := auditAuthn(t, nil, next)
	h.ServeHTTP(httptest.NewRecorder(), auditRequest(hs256Token(t, auditTestSecret, "alice")))
	if principal != "alice" {
		t.Fatalf("principal = %v", principal)
	}
}

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

func auditAuthn(t *testing.T, trusted []string, next http.Handler) (http.Handler, *[]audit.Event) {
	t.Helper()
	var got []audit.Event
	cfg := config.Defaults()
	cfg.Authn.Enabled = true
	cfg.Authn.TrustedSigners = []string{auditTestSecret}
	cfg.Authn.TrustedIDs = trusted
	h := AuthnJWT(cfg, nil, next, logging.New(logging.Config{Out: io.Discard}),
		WithAudit(func(e audit.Event) { got = append(got, e) }))
	return h, &got
}

const auditTestSecret = "0123456789abcdef0123456789abcdef"
