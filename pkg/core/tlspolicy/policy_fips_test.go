//go:build !no_fips

package tlspolicy

import (
	"crypto/tls"
	"slices"
	"testing"
)

// A FIPS-policy build never offers X25519 outbound.
func TestClientConfig_FIPSCurves(t *testing.T) {
	c := ClientConfig()
	if !slices.Equal(c.CurvePreferences, []tls.CurveID{tls.CurveP256, tls.CurveP384}) {
		t.Fatalf("curves = %v, want P-256, P-384", c.CurvePreferences)
	}
}
