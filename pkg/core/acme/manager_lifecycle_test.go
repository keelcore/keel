//go:build !no_acme

package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// certNeedsRenewal — cert with invalid/unparseable DER bytes
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// assembleTLSCert — empty DER chain yields error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// storeCert — assembleTLSCert failure (empty DER chain)
// ---------------------------------------------------------------------------

// storeCert — MkdirAll failure (non-existent parent directory)
// ---------------------------------------------------------------------------

// storeCert — validation failure (domain mismatch)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// writeCertPEM — error writing to non-writable path
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// writeKeyPEM — error writing to non-writable path
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// SetLogger
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// CertExpiry
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GetCertificate
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildCSR
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// assembleTLSCert
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// writeCertPEM
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// renewalDelay
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// httpClientWithCA
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// storeCert — no cache dir
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// loadOrCreateAccountKey
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// loadKeyPEM
// ---------------------------------------------------------------------------

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
