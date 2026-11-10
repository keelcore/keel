//go:build !no_acme

package acme

import (
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
)

// Internal tests for cache helpers (loadCachedCert, certNeedsRenewal,
// validateCert) and the Start() cache-load branch. These are in package acme
// (not acme_test) because the helpers are unexported.

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

// ---------------------------------------------------------------------------
// certNeedsRenewal
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// validateCert
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Start() — cache-load branch (items 3 and 7)
// ---------------------------------------------------------------------------
