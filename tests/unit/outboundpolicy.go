package unit

import (
	"crypto/tls"
	"net/http"
	"testing"
)

// AssertOutboundPolicy reports, under name, a client c that does not use the outbound TLS policy: an
// *http.Transport whose TLS configuration requires TLS 1.3.
func AssertOutboundPolicy(t testing.TB, name string, c *http.Client) {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Errorf("%s: transport = %#v, want the outbound TLS policy", name, c.Transport)
	}
}
