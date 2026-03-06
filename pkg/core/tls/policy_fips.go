//go:build !no_fips

package tls

import (
	"crypto/tls"

	"github.com/keelcore/keel/pkg/config"
)

func BuildTLSConfig(_ config.Config) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Restrict key-exchange curves to FIPS 140-2/3 approved curves only.
		// X25519 is excluded; BoringCrypto enforces AES-GCM-only for TLS 1.3 ciphers.
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.CurveP384},
	}
}
