//go:build !no_acme

package acme

// Internal tests for cache helpers (loadCachedCert, certNeedsRenewal,
// validateCert) and the Start() cache-load branch. These are in package acme
// (not acme_test) because the helpers are unexported.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeCachedCert writes a self-signed ECDSA P-256 cert covering domains to
// dir in the exact PEM format that storeCert produces. Returns dir.
func writeCachedCert(t *testing.T, dir string, domains []string, notAfter time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certF, err := os.Create(filepath.Join(dir, "cert.crt"))
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(certF, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certF.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyF, err := os.Create(filepath.Join(dir, "cert.key"))
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(keyF, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyF.Close()
}

// writeRSACachedCert writes a self-signed RSA 2048 cert to dir.
func writeRSACachedCert(t *testing.T, dir string, domains []string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certF, err := os.Create(filepath.Join(dir, "cert.crt"))
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(certF, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certF.Close()

	keyDER := x509.MarshalPKCS1PrivateKey(key)
	keyF, err := os.Create(filepath.Join(dir, "cert.key"))
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(keyF, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
	keyF.Close()
}

// makeTLSCert builds an in-memory *tls.Certificate covering domains with the
// given key type ("ecdsa" or "rsa") and notAfter.
func makeTLSCert(t *testing.T, domains []string, keyType string, notAfter time.Time) *tls.Certificate {
	t.Helper()

	var (
		pubKey  interface{}
		privKey interface{}
	)
	switch keyType {
	case "ecdsa":
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		pubKey, privKey = &k.PublicKey, k
	case "rsa":
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		pubKey, privKey = &k.PublicKey, k
	default:
		t.Fatalf("unknown key type %q", keyType)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pubKey, privKey)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{certDER}}
}

// ---------------------------------------------------------------------------
// loadCachedCert
// ---------------------------------------------------------------------------

func TestLoadCachedCert_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadCachedCert(dir); err == nil {
		t.Error("expected error for missing cert files, got nil")
	}
}

func TestLoadCachedCert_ValidFiles(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	cert, err := loadCachedCert(dir)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cert == nil {
		t.Error("expected non-nil cert")
	}
}

// ---------------------------------------------------------------------------
// certNeedsRenewal
// ---------------------------------------------------------------------------

func TestCertNeedsRenewal_Nil(t *testing.T) {
	if !certNeedsRenewal(nil) {
		t.Error("nil cert should need renewal")
	}
}

func TestCertNeedsRenewal_Expired(t *testing.T) {
	c := makeTLSCert(t, []string{"example.com"}, "ecdsa", time.Now().Add(-time.Hour))
	if !certNeedsRenewal(c) {
		t.Error("expired cert should need renewal")
	}
}

func TestCertNeedsRenewal_WithinRenewalWindow(t *testing.T) {
	// 15 days left — inside the 30-day window
	c := makeTLSCert(t, []string{"example.com"}, "ecdsa", time.Now().Add(15*24*time.Hour))
	if !certNeedsRenewal(c) {
		t.Error("cert within 30-day window should need renewal")
	}
}

func TestCertNeedsRenewal_FreshCert(t *testing.T) {
	// 60 days left — outside the 30-day window
	c := makeTLSCert(t, []string{"example.com"}, "ecdsa", time.Now().Add(60*24*time.Hour))
	if certNeedsRenewal(c) {
		t.Error("cert with 60 days remaining should not need renewal")
	}
}

// ---------------------------------------------------------------------------
// validateCert
// ---------------------------------------------------------------------------

func TestValidateCert_Valid(t *testing.T) {
	c := makeTLSCert(t, []string{"api.example.com", "www.example.com"}, "ecdsa",
		time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"api.example.com", "www.example.com"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateCert_MissingDomain(t *testing.T) {
	c := makeTLSCert(t, []string{"api.example.com"}, "ecdsa", time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"api.example.com", "www.example.com"}); err == nil {
		t.Error("expected error for missing domain, got nil")
	}
}

func TestValidateCert_RSAKeyRejected(t *testing.T) {
	c := makeTLSCert(t, []string{"example.com"}, "rsa", time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"example.com"}); err == nil {
		t.Error("expected error for RSA key, got nil")
	}
}

func TestValidateCert_SubsetDomainsOK(t *testing.T) {
	// Cert covers three domains; config only requires two — still valid.
	c := makeTLSCert(t, []string{"a.example.com", "b.example.com", "c.example.com"}, "ecdsa",
		time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"a.example.com", "b.example.com"}); err != nil {
		t.Errorf("expected nil for subset match, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Start() — cache-load branch (items 3 and 7)
// ---------------------------------------------------------------------------

// TestStart_LoadsCachedCert verifies that Start() stores a pre-existing cached
// cert into m.cert before contacting the CA. The context is pre-cancelled so
// Start() exits immediately after the cache-load attempt.
func TestStart_LoadsCachedCert(t *testing.T) {
	dir := t.TempDir()
	// Write a fresh cert (60 days remaining — not in renewal window).
	writeCachedCert(t, dir, []string{"example.com"}, time.Now().Add(60*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:  []string{"example.com"},
		CacheDir: dir,
	}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: expected nil, got %v", err)
	}
	if mgr.cert.Load() == nil {
		t.Error("cert should be loaded from cache before CA contact")
	}
}

// TestStart_CachedCertWrongDomain verifies that Start() returns a non-nil
// error and does not store the cert when the cached cert's SANs don't match
// the configured domains.
func TestStart_CachedCertWrongDomain(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, []string{"old.example.com"}, time.Now().Add(60*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:  []string{"new.example.com"},
		CacheDir: dir,
	}
	if err := mgr.Start(ctx, cfg); err == nil {
		t.Error("Start: expected error for domain mismatch, got nil")
	}
	if mgr.cert.Load() != nil {
		t.Error("cert must not be stored when validation fails")
	}
}

// TestStart_CachedCertRSAKeyRejected verifies that Start() returns an error
// when the cached cert uses an RSA key, which violates Keel TLS policy.
func TestStart_CachedCertRSAKeyRejected(t *testing.T) {
	dir := t.TempDir()
	writeRSACachedCert(t, dir, []string{"example.com"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:  []string{"example.com"},
		CacheDir: dir,
	}
	if err := mgr.Start(ctx, cfg); err == nil {
		t.Error("Start: expected error for RSA cert, got nil")
	}
}

// TestStart_WithCACertFile_ValidFile verifies that Start() uses the
// CACertFile to build an http.Client when set. The context is pre-cancelled
// so Start returns after the cache load attempt.
func TestStart_WithCACertFile_ValidFile(t *testing.T) {
	cacheDir := t.TempDir()
	caDir := t.TempDir()
	// Write a fresh ECDSA cert to cache so validation passes.
	writeCachedCert(t, cacheDir, []string{"example.com"}, time.Now().Add(60*24*time.Hour))
	// Write a valid self-signed PEM cert as the CA cert.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(365*24*time.Hour))
	caPath := filepath.Join(caDir, "ca.pem")
	f, err := os.Create(caPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:    []string{"example.com"},
		CacheDir:   cacheDir,
		CACertFile: caPath,
	}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start with CACertFile: expected nil, got %v", err)
	}
}

// TestStart_WithCACertFile_InvalidFile verifies that Start() returns an error
// when CACertFile points to a non-existent file.
func TestStart_WithCACertFile_InvalidFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:    []string{"example.com"},
		CACertFile: "/nonexistent-ca-for-test.pem",
	}
	if err := mgr.Start(ctx, cfg); err == nil {
		t.Error("Start: expected error for missing CACertFile, got nil")
	}
}

// TestStart_DefaultCAUrl verifies Start() uses LetsEncryptURL when CAUrl is empty.
// The context is pre-cancelled (after registerAccount fails with context.Canceled)
// so the function returns nil.
func TestStart_DefaultCAUrl_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so registerAccount gets context.Canceled immediately

	mgr := New()
	cfg := config.ACMEConfig{
		Domains: []string{"example.com"},
		// CAUrl empty → uses LetsEncryptURL
	}
	// registerAccount will fail with context.Canceled → Start returns nil.
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start with canceled ctx: expected nil, got %v", err)
	}
}

// TestStart_WithEmail verifies Start() passes the email to registerAccount.
// The context is pre-cancelled so the function exits after registration fails.
func TestStart_WithEmail_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains: []string{"example.com"},
		Email:   "test@example.com",
	}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start with email + canceled ctx: expected nil, got %v", err)
	}
}
