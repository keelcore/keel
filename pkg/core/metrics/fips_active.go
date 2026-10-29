package metrics

import "github.com/keelcore/keel/pkg/core/version"

// fipsActive is fixed for the process lifetime (FIPS mode is set at start).
var fipsActive = boolGauge(version.FIPSActive())

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
