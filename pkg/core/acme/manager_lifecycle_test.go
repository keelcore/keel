//go:build !no_acme

package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// certNeedsRenewal — cert with invalid/unparseable DER bytes
// ---------------------------------------------------------------------------

func TestCertNeedsRenewal_BadCertBytes(t *testing.T) {
	// A tls.Certificate with garbage DER in Certificate[0] — ParseCertificate
	// will fail, so certNeedsRenewal must return true.
	c := &tls.Certificate{Certificate: [][]byte{[]byte("not-a-real-cert")}}
	if !certNeedsRenewal(c) {
		t.Error("cert with invalid DER should need renewal")
	}
}

// ---------------------------------------------------------------------------
// assembleTLSCert — empty DER chain yields error
// ---------------------------------------------------------------------------

func TestAssembleTLSCert_EmptyChain_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// An empty certPEM fed to tls.X509KeyPair should fail.
	if _, err := assembleTLSCert(key, [][]byte{}); err == nil {
		t.Error("expected error for empty DER chain")
	}
}

// ---------------------------------------------------------------------------
// storeCert — assembleTLSCert failure (empty DER chain)
// ---------------------------------------------------------------------------

func TestStoreCert_AssembleFailure(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Empty DER chain causes assembleTLSCert to fail (X509KeyPair needs cert).
	if err := m.storeCert("", []string{"example.com"}, key, [][]byte{}); err == nil {
		t.Error("expected error for empty DER chain in storeCert")
	}
}

// storeCert — MkdirAll failure (non-existent parent directory)
// ---------------------------------------------------------------------------

func TestStoreCert_MkdirFail(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	// Use a path under an existing read-only root file to trigger MkdirAll failure.
	// /dev/null is a file, so creating a subdir under it fails on all platforms.
	if err := m.storeCert("/dev/null/nonexistent-cache-for-test", []string{"example.com"}, key, [][]byte{der}); err == nil {
		t.Error("expected error for non-writable cacheDir in storeCert")
	}
}

// storeCert — validation failure (domain mismatch)
// ---------------------------------------------------------------------------

func TestStoreCert_ValidationFailure(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Cert covers "example.com" but we claim it should cover "other.com".
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	if err := m.storeCert("", []string{"other.com"}, key, [][]byte{der}); err == nil {
		t.Error("expected error when cert SANs do not match configured domains")
	}
}

// ---------------------------------------------------------------------------
// writeCertPEM — error writing to non-writable path
// ---------------------------------------------------------------------------

func TestWriteCertPEM_NonWritablePath_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(time.Hour))
	// Write to a non-existent directory — should fail.
	err = writeCertPEM("/nonexistent-dir-for-test/cert.crt", [][]byte{der})
	if err == nil {
		t.Error("expected error writing to non-existent directory")
	}
}

// ---------------------------------------------------------------------------
// writeKeyPEM — error writing to non-writable path
// ---------------------------------------------------------------------------

func TestWriteKeyPEM_NonWritablePath_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	err = writeKeyPEM("/nonexistent-dir-for-test/cert.key", key)
	if err == nil {
		t.Error("expected error writing to non-existent directory")
	}
}

// ---------------------------------------------------------------------------
// SetLogger
// ---------------------------------------------------------------------------

func TestSetLogger_StoredAndCalled(t *testing.T) {
	m := New()
	called := false
	m.SetLogger(func(event string, _ map[string]any) {
		called = true
		_ = event
	})
	if m.logErr == nil {
		t.Error("expected logErr to be set after SetLogger")
	}
	m.logErr("test_event", nil)
	if !called {
		t.Error("expected logger callback to be invoked")
	}
}

// ---------------------------------------------------------------------------
// CertExpiry
// ---------------------------------------------------------------------------

func TestCertExpiry_NoCert(t *testing.T) {
	m := New()
	_, err := m.CertExpiry()
	if err == nil {
		t.Error("expected error when no certificate stored")
	}
}

func TestCertExpiry_WithCert(t *testing.T) {
	m := New()
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	m.cert.Store(c)
	secs, err := m.CertExpiry()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if secs <= 0 {
		t.Errorf("expected positive expiry seconds, got %f", secs)
	}
}

// ---------------------------------------------------------------------------
// GetCertificate
// ---------------------------------------------------------------------------

func TestGetCertificate_NoCert(t *testing.T) {
	m := New()
	_, err := m.GetCertificate(nil)
	if err == nil {
		t.Error("expected error when no certificate stored")
	}
}

func TestGetCertificate_WithCert(t *testing.T) {
	m := New()
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	m.cert.Store(c)
	got, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got == nil {
		t.Error("expected non-nil certificate")
	}
}

// ---------------------------------------------------------------------------
// buildCSR
// ---------------------------------------------------------------------------

func TestBuildCSR_Valid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	domains := []string{"example.com", "www.example.com"}
	der, err := buildCSR(key, domains)
	if err != nil {
		t.Fatalf("buildCSR: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatalf("parse CSR: %v", err)
	}
	if csr.Subject.CommonName != domains[0] {
		t.Errorf("expected CN %q, got %q", domains[0], csr.Subject.CommonName)
	}
	if len(csr.DNSNames) != len(domains) {
		t.Errorf("expected %d DNS names, got %d", len(domains), len(csr.DNSNames))
	}
}

// ---------------------------------------------------------------------------
// assembleTLSCert
// ---------------------------------------------------------------------------

func TestAssembleTLSCert_Valid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(time.Hour))
	cert, err := assembleTLSCert(key, [][]byte{der})
	if err != nil {
		t.Fatalf("assembleTLSCert: %v", err)
	}
	if cert == nil {
		t.Error("expected non-nil tls.Certificate")
	}
}

// ---------------------------------------------------------------------------
// writeCertPEM
// ---------------------------------------------------------------------------

func TestWriteCertPEM_CreatesFile(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(time.Hour))

	dir := t.TempDir()
	path := filepath.Join(dir, "cert.crt")
	if err := writeCertPEM(path, [][]byte{der}); err != nil {
		t.Fatalf("writeCertPEM: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cert file: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Error("no PEM block in written cert file")
	}
}

// ---------------------------------------------------------------------------
// renewalDelay
// ---------------------------------------------------------------------------

func TestRenewalDelay_NilCert(t *testing.T) {
	d := renewalDelay(nil)
	if d != 24*time.Hour {
		t.Errorf("expected 24h fallback for nil cert, got %v", d)
	}
}

func TestRenewalDelay_FreshCert(t *testing.T) {
	// 90 days remaining — delay should be ~60 days (90 - 30).
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	d := renewalDelay(c)
	// Should be positive and greater than 1 hour.
	if d < time.Hour {
		t.Errorf("expected delay > 1h for fresh cert, got %v", d)
	}
}

func TestRenewalDelay_ExpiringSoon(t *testing.T) {
	// 10 days remaining — inside the 30-day window → minimum 1h.
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(10*24*time.Hour))
	d := renewalDelay(c)
	if d != time.Hour {
		t.Errorf("expected 1h minimum for expiring cert, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// httpClientWithCA
// ---------------------------------------------------------------------------

func TestHTTPClientWithCA_MissingFile(t *testing.T) {
	_, err := httpClientWithCA("/nonexistent-ca-for-test.pem")
	if err == nil {
		t.Error("expected error for missing CA cert file")
	}
}

func TestHTTPClientWithCA_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := httpClientWithCA(path)
	if err == nil {
		t.Error("expected error for invalid PEM content")
	}
}

func TestHTTPClientWithCA_ValidCA(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"ca.example.com"}, time.Now().Add(365*24*time.Hour))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.Close()

	client, err := httpClientWithCA(path)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if client == nil {
		t.Error("expected non-nil http.Client")
	}
}

// ---------------------------------------------------------------------------
// storeCert — no cache dir
// ---------------------------------------------------------------------------

func TestStoreCert_NoCacheDir(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	if err := m.storeCert("", []string{"example.com"}, key, [][]byte{der}); err != nil {
		t.Fatalf("storeCert with empty cacheDir: %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("cert must be stored in memory even with empty cacheDir")
	}
}

func TestStoreCert_WithCacheDir(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	if err := m.storeCert(dir, []string{"example.com"}, key, [][]byte{der}); err != nil {
		t.Fatalf("storeCert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cert.crt")); err != nil {
		t.Errorf("cert.crt not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cert.key")); err != nil {
		t.Errorf("cert.key not written: %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadOrCreateAccountKey
// ---------------------------------------------------------------------------

// loadOrCreateAccountKey with empty cacheDir generates a new key.
func TestLoadOrCreateAccountKey_EmptyCacheDir(t *testing.T) {
	key, err := loadOrCreateAccountKey("")
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey with empty cacheDir: %v", err)
	}
	if key == nil {
		t.Error("expected non-nil key")
	}
}

// loadOrCreateAccountKey creates and caches a key in a temp dir.
func TestLoadOrCreateAccountKey_CreatesAndCaches(t *testing.T) {
	dir := t.TempDir()
	key1, err := loadOrCreateAccountKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey (create): %v", err)
	}
	if key1 == nil {
		t.Fatal("expected non-nil key after create")
	}

	// Second call should load from cache.
	key2, err := loadOrCreateAccountKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey (load): %v", err)
	}
	if key2 == nil {
		t.Error("expected non-nil key after load")
	}
	// Both keys should have the same public key.
	if key1.PublicKey.X.Cmp(key2.PublicKey.X) != 0 {
		t.Error("expected same key on second call (loaded from cache)")
	}
}

// ---------------------------------------------------------------------------
// loadKeyPEM
// ---------------------------------------------------------------------------

// loadKeyPEM with a missing file returns an error.
func TestLoadKeyPEM_MissingFile(t *testing.T) {
	_, err := loadKeyPEM("/nonexistent-key-for-test.pem")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// loadKeyPEM with non-PEM content returns an error.
func TestLoadKeyPEM_NotPEM(t *testing.T) {
	dir := t.TempDir()
	path := makeFile(t, dir, "notpem.pem", []byte("not a pem"))
	_, err := loadKeyPEM(path)
	if err == nil {
		t.Error("expected error for non-PEM content")
	}
}

// loadKeyPEM with a valid EC key PEM returns the key.
func TestLoadKeyPEM_ValidKey(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := makeFile(t, dir, "key.pem", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	loaded, err := loadKeyPEM(path)
	if err != nil {
		t.Fatalf("loadKeyPEM: %v", err)
	}
	if loaded == nil {
		t.Error("expected non-nil key")
	}
}

// makeFile writes content to dir/name and returns the path.
func makeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// makeMgrTLSCert builds an in-memory *tls.Certificate with an ECDSA P-256 key.
func makeMgrTLSCert(t *testing.T, domains []string, notAfter time.Time) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, domains, notAfter)
	return &tls.Certificate{Certificate: [][]byte{der}}
}

// makeECDSACertDER returns a self-signed DER-encoded certificate.
func makeECDSACertDER(t *testing.T, key *ecdsa.PrivateKey, domains []string, notAfter time.Time) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
