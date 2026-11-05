package tlspolicy

import (
	"crypto/tls"
	"net/http"
	"time"
)

// ClientConfig is a fresh copy of the policy for an outbound connection. Callers layer RootCAs,
// ServerName and client certificates on top.
func ClientConfig() *tls.Config {
	return Base()
}

// NewHTTPTransport clones http.DefaultTransport (dial, handshake and idle timeouts, HTTP/2) with
// the policy as its TLS configuration.
func NewHTTPTransport() *http.Transport {
	t := &http.Transport{ForceAttemptHTTP2: true}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		t = dt.Clone()
	}
	t.TLSClientConfig = ClientConfig()
	return t
}

// NewHTTPClient is an http.Client with the policy's transport and the given overall timeout
// (0: none).
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: NewHTTPTransport(), Timeout: timeout}
}
