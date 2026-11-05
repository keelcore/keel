//go:build !no_remotelog && !windows

package logging

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestHTTPSink_UsesTheOutboundTLSPolicy(t *testing.T) {
	c := NewHTTPSink("https://logs.example", 1, time.Second).client
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("transport = %#v, want the outbound TLS policy", c.Transport)
	}
}
