package tlspolicy

import (
	"crypto/tls"
	"net/http"
	"slices"
	"testing"
	"time"
)

// Callers layer RootCAs, ServerName and client certs on their own copy.
func TestClientConfig_IsACopy(t *testing.T) {
	a, b := ClientConfig(), ClientConfig()
	a.ServerName = "x"
	if b.ServerName != "" {
		t.Fatal("ClientConfig shares state between callers")
	}
}

func TestNewHTTPTransport(t *testing.T) {
	tr := NewHTTPTransport()
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("TLSClientConfig = %+v, want the policy", tr.TLSClientConfig)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Fatal("HTTP/2 must stay available with a custom TLS config")
	}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok && dt.TLSClientConfig == tr.TLSClientConfig {
		t.Fatal("the default transport was modified")
	}
}

func TestNewHTTPClient(t *testing.T) {
	c := NewHTTPClient(3 * time.Second)
	if c.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %s", c.Timeout)
	}
	if tr, ok := c.Transport.(*http.Transport); !ok || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("Transport = %#v, want the policy", c.Transport)
	}
}

// A FIPS-policy build never offers X25519 outbound.
func TestClientConfig_FIPSCurves(t *testing.T) {
	c := ClientConfig()
	if !slices.Equal(c.CurvePreferences, []tls.CurveID{tls.CurveP256, tls.CurveP384}) {
		t.Fatalf("curves = %v, want P-256, P-384", c.CurvePreferences)
	}
}

func TestClientConfig_IsTheListenerPolicy(t *testing.T) {
	c := ClientConfig()
	if c.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %x, want TLS 1.3", c.MinVersion)
	}
	if b := Base(); len(c.CurvePreferences) != len(b.CurvePreferences) {
		t.Fatalf("curves %v, policy %v", c.CurvePreferences, b.CurvePreferences)
	}
}
