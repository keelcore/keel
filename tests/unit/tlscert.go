// Package unit holds unit-test helpers that tests in more than one keel package share. It imports only the
// standard library, so any package's tests can import it without an import cycle.
package unit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// generateKey, createCertificate and marshalECPrivateKey are seams over the crypto calls TLSTestCert makes,
// so tests can reach its error branches. They default to the real functions.
var (
	generateKey         = ecdsa.GenerateKey
	createCertificate   = x509.CreateCertificate
	marshalECPrivateKey = x509.MarshalECPrivateKey
)

// TLSTestCert returns a self-signed ECDSA P-256 certificate for 127.0.0.1, valid until notAfter, and its
// key, both PEM-encoded.
func TLSTestCert(t testing.TB, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := generateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "keel-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := createCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := marshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}
