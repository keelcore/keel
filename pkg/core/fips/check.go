//go:build !no_fips

// Package fips provides runtime FIPS posture verification.
// It is active in all builds except those compiled with the no_fips tag.
package fips

import (
	"fmt"
	"os"
	"strings"
)

// Check verifies that the FIPS 140 runtime mode is active. It returns nil
// when at least one of the following conditions holds:
//
//   - GOFIPS140 is set to a non-empty value, or
//   - GODEBUG contains the token "fips140=only".
//
// If neither condition holds, Check returns a descriptive error. Callers
// should treat a non-nil return as a fatal misconfiguration when fips.monitor
// is enabled.
func Check() error {
	if v := os.Getenv("GOFIPS140"); v != "" {
		return nil
	}
	for _, token := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if strings.TrimSpace(token) == "fips140=only" {
			return nil
		}
	}
	return fmt.Errorf(
		"FIPS runtime mode is not active: set GOFIPS140=1 or GODEBUG=fips140=only before starting this binary",
	)
}
