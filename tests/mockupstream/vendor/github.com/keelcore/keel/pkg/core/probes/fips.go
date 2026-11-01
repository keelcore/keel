package probes

import "github.com/keelcore/keel/pkg/core/version"

// fipsActive is fixed for the process lifetime (FIPS mode is set at start).
var fipsActive = version.FIPSActive()
