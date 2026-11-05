//go:build !no_fips

package tlspolicy

import "crypto/tls"

// Base is keel's TLS policy: TLS 1.3 only, with FIPS-approved key exchange.
func Base() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Restrict key-exchange curves to FIPS 140-2/3 approved curves only.
		// X25519 is excluded. TLS 1.3 suites are fixed by the protocol; in FIPS 140
		// mode the Go Cryptographic Module negotiates only the AES-GCM suites.
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.CurveP384},
	}
}
