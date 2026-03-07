//go:build fips

package main

import (
	"fmt"
	"os"
	"strings"
)

// init runs before main() in FIPS builds and enforces that the runtime is
// operating in FIPS 140 mode. If GODEBUG=fips140=only is absent, the binary
// fails closed to prevent accidental non-compliant operation.
func init() {
	if !hasFIPSMode() {
		fmt.Fprintln(os.Stderr, "Failing closed: FIPS build requires GODEBUG=fips140=only")
		os.Exit(1)
	}
}

// hasFIPSMode splits GODEBUG on commas and checks for an exact "fips140=only"
// entry, avoiding false positives from substring matches.
func hasFIPSMode() bool {
	for _, kv := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if strings.TrimSpace(kv) == "fips140=only" {
			return true
		}
	}
	return false
}