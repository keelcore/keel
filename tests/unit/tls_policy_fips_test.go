//go:build !no_fips

package unit

import (
	"crypto/tls"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// TestBuildTLSConfig_FIPSCurves verifies that the FIPS build restricts curve
// preferences to FIPS-approved curves (P-256 and P-384) and excludes X25519,
// which is not approved under FIPS 140-2/3.
func TestBuildTLSConfig_FIPSCurves(t *testing.T) {
	cfg := keeltls.BuildTLSConfig(config.Config{})

	if len(cfg.CurvePreferences) == 0 {
		t.Fatal("FIPS build must set CurvePreferences to restrict to approved curves")
	}

	for _, curve := range cfg.CurvePreferences {
		switch curve {
		case tls.CurveP256, tls.CurveP384:
			// approved
		default:
			t.Errorf("FIPS build includes non-FIPS curve: %v", curve)
		}
	}
}
