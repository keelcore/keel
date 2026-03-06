//go:build no_fips

package tls

import (
	"crypto/tls"

	"github.com/keelcore/keel/pkg/config"
)

func BuildTLSConfig(_ config.Config) *tls.Config {
	return &tls.Config{
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
	}
}
