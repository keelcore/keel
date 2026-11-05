//go:build !no_otel

package tracing

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

func TestExporter_UsesTheOutboundTLSPolicy(t *testing.T) {
	e, err := Setup(config.OTLPConfig{Enabled: true, Endpoint: "collector.example:4318"})
	if err != nil || e == nil {
		t.Fatalf("Setup: %v, %v", e, err)
	}
	defer Shutdown(e)
	tr, ok := e.client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("transport = %#v, want the outbound TLS policy", e.client.Transport)
	}
}
