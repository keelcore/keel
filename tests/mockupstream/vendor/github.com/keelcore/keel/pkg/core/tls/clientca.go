package tls

import (
	"crypto/x509"
	"fmt"
	"os"
)

// LoadClientCAs reads a PEM bundle of CAs trusted to sign inbound client
// certificates. A missing file or one without certificates is an error.
func LoadClientCAs(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read client_ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("parse client_ca_file %q: no certificates found", path)
	}
	return pool, nil
}
