//go:build !no_authn && !no_authz

package mw

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

func assertOutboundPolicy(t *testing.T, name string, c *http.Client) {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Errorf("%s: transport = %#v, want the outbound TLS policy", name, c.Transport)
	}
}

func TestOutboundClients_UseTheTLSPolicy(t *testing.T) {
	assertOutboundPolicy(t, "jwks", jwksClient)
	assertOutboundPolicy(t, "authz https", authzClient(config.ExtAuthzConfig{Endpoint: "https://opa.example"}))
	assertOutboundPolicy(t, "authz unix", authzClient(config.ExtAuthzConfig{Endpoint: "unix:///tmp/opa.sock"}))
}
