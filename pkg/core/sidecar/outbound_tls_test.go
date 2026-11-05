//go:build !no_sidecar

package sidecar

import (
	"crypto/tls"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

// An https upstream gets the policy whether or not sidecar.upstream_tls is enabled.
func TestBuildTransport_UsesTheTLSPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("enabled=%v: TLSClientConfig = %+v, want the outbound TLS policy", enabled, tr.TLSClientConfig)
		}
	}
}
