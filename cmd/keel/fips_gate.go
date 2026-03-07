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
	if !strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
		fmt.Fprintln(os.Stderr, "Failing closed: FIPS build requires GODEBUG=fips140=only")
		os.Exit(1)
	}
}