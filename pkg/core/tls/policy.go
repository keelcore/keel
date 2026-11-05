package tls

import (
	"crypto/tls"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/tlspolicy"
)

// BuildTLSConfig is the listener configuration: keel's one TLS policy (pkg/core/tlspolicy).
func BuildTLSConfig(_ config.Config) *tls.Config {
	return tlspolicy.Base()
}
