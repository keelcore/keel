package tls

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"
)

// CertExpiry returns the NotAfter time of the first leaf certificate in certFile.
// certFile must be a PEM-encoded certificate (chains are supported; only the
// first block is inspected).
func CertExpiry(certFile string) (time.Time, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return time.Time{}, fmt.Errorf("read cert %q: %w", certFile, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block found in %q", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cert %q: %w", certFile, err)
	}
	return cert.NotAfter, nil
}

// CertExpirySeconds returns the number of seconds until the leaf certificate
// in certFile expires. The value is negative if the certificate has already
// expired.
func CertExpirySeconds(certFile string) (float64, error) {
	expiry, err := CertExpiry(certFile)
	if err != nil {
		return 0, err
	}
	return time.Until(expiry).Seconds(), nil
}