package config

import (
	"strings"
	"testing"
)

// upstream_tls is incompatible with a unix:// upstream and with plaintext h2c.
func TestValidate_SidecarUpstreamTLSConflicts(t *testing.T) {
	base := Defaults()
	base.Sidecar.Enabled = true
	base.Sidecar.UpstreamURL = "unix:///run/app.sock"
	base.Sidecar.UpstreamH2C = true
	if err := Validate(base); err != nil {
		t.Fatalf("unix socket + h2c rejected: %v", err)
	}

	sockTLS := base
	sockTLS.Sidecar.UpstreamH2C = false
	sockTLS.Sidecar.UpstreamTLS.Enabled = true
	if err := Validate(sockTLS); err == nil || !strings.Contains(err.Error(), "unix://") {
		t.Errorf("unix + tls: err = %v", err)
	}

	h2cTLS := base
	h2cTLS.Sidecar.UpstreamURL = "https://app:8443"
	h2cTLS.Sidecar.UpstreamTLS.Enabled = true
	if err := Validate(h2cTLS); err == nil || !strings.Contains(err.Error(), "upstream_h2c") {
		t.Errorf("h2c + tls: err = %v", err)
	}
}
