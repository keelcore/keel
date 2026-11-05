//go:build no_fips

package tlspolicy

import "crypto/tls"

// Base is keel's TLS policy: TLS 1.3 only.
func Base() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
	}
}
