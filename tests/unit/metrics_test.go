//go:build !no_prom

package unit

import (
	"testing"

	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/version"
)

func TestMetrics_FIPSActive(t *testing.T) {
	m := metrics.New()
	v := m.FIPSActive()
	if v != 0 && v != 1 {
		t.Errorf("keel_fips_active must be 0 or 1, got %g", v)
	}
	// It reports runtime FIPS mode (FIPS build + crypto/fips140.Enabled), the
	// same answer as /version — not merely the build tag.
	want := 0.0
	if version.FIPSActive() {
		want = 1
	}
	if v != want {
		t.Errorf("keel_fips_active = %g, want %g (version.FIPSActive)", v, want)
	}
}
