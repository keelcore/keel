//go:build !no_acme

package acme

import (
	"crypto/tls"
	"net/http"
	"testing"
)

func assertOutboundPolicy(t *testing.T, name string, c *http.Client) *tls.Config {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("%s: transport = %#v, want the outbound TLS policy", name, c.Transport)
	}
	return tr.TLSClientConfig
}
