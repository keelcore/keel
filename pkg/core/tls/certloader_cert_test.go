package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	stdtls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genCertKey writes a self-signed ECDSA cert and key into dir and returns the
// cert path, key path, and the certificate NotAfter time.
func genCertKey(t *testing.T, dir string, notAfter time.Time) (string, string, time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, notAfter
}

func TestCertExpiry(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("fips")
	}
	dir := t.TempDir()
	want := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	certPath, _, _ := genCertKey(t, dir, want)

	got, err := CertExpiry(certPath)
	if err != nil {
		t.Fatalf("CertExpiry: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("expiry = %v, want %v", got, want)
	}
}

func TestCertExpiryReadError(t *testing.T) {
	if _, err := CertExpiry(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCertExpiryNoPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(path, []byte("not a pem block"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CertExpiry(path); err == nil {
		t.Fatal("expected error for non-PEM content")
	}
}

func TestCertExpiryParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-der")})
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CertExpiry(path); err == nil {
		t.Fatal("expected error for unparseable cert")
	}
}

func TestCertExpirySeconds(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("fips")
	}
	dir := t.TempDir()
	certPath, _, _ := genCertKey(t, dir, time.Now().Add(24*time.Hour))

	secs, err := CertExpirySeconds(certPath)
	if err != nil {
		t.Fatalf("CertExpirySeconds: %v", err)
	}
	if secs <= 0 {
		t.Fatalf("seconds = %v, want > 0", secs)
	}
}

func TestCertExpirySecondsError(t *testing.T) {
	if _, err := CertExpirySeconds(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNewCertLoaderAndGet(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("fips")
	}
	dir := t.TempDir()
	certPath, keyPath, _ := genCertKey(t, dir, time.Now().Add(24*time.Hour))

	l, err := NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	got, err := l.Get(&stdtls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil certificate")
	}

	// Reload with a fresh cert should succeed and swap the stored cert.
	dir2 := t.TempDir()
	certPath2, keyPath2, _ := genCertKey(t, dir2, time.Now().Add(48*time.Hour))
	if err := l.Reload(certPath2, keyPath2); err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

func TestCertLoaderReloadError(t *testing.T) {
	if _, err := NewCertLoader("missing-cert.pem", "missing-key.pem"); err == nil {
		t.Fatal("expected error for missing cert/key")
	}
}

func TestCertLoaderGetNoCert(t *testing.T) {
	l := &CertLoader{}
	if _, err := l.Get(&stdtls.ClientHelloInfo{}); err == nil {
		t.Fatal("expected error when no certificate loaded")
	}
}
