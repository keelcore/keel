//go:build !no_fips

// Package fips provides runtime FIPS posture verification.
// It is active in all builds except those compiled with the no_fips tag.
package fips

import (
	"crypto/fips140"
	"errors"
)

// fipsEnabled reports whether the Go Cryptographic Module runs in FIPS 140
// mode. A seam over crypto/fips140.Enabled so tests can drive both outcomes.
var fipsEnabled = fips140.Enabled

// Check verifies that FIPS 140 mode is actually active in this process, as
// reported by crypto/fips140.Enabled — true for a binary built with GOFIPS140
// (make max) or run with GODEBUG=fips140=on|only.
//
// Environment variables are not consulted: GOFIPS140 only takes effect at build
// time (GOFIPS140=off is non-empty yet disables FIPS), and GODEBUG is read at
// process start, so inspecting either at runtime can report FIPS that is not
// there. Callers should treat a non-nil return as a fatal misconfiguration
// when fips.monitor is enabled.
func Check() error {
	if fipsEnabled() {
		return nil
	}
	return errors.New(
		"FIPS 140 mode is not active: build with GOFIPS140 (make max) or run with GODEBUG=fips140=on or fips140=only",
	)
}
