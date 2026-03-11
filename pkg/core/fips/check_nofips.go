//go:build no_fips

// Package fips provides runtime FIPS posture verification.
// This stub is compiled when the no_fips build tag is active.
package fips

import "fmt"

// Check always returns an error on no_fips binaries. The binary was compiled
// without BoringCrypto; no environment variable can retroactively enable FIPS
// cryptography in a running process.
func Check() error {
	return fmt.Errorf(
		"binary was not compiled with FIPS support (no_fips build tag is active); " +
			"use a FIPS build to enable fips.monitor",
	)
}
