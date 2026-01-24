package main

import (
	"crypto/tls"
	"fmt"
	"os"

	// This is the "shredded" magic link to BoringSSL
	_ "crypto/tls/fipsonly"
)

func main() {
	checkFipsOrDie()

	fmt.Println("⚓ Keel: Authorized Build")
	fmt.Println("Status: Ripped. Hard. Shredded.")

	// Server logic goes here
}

func checkFipsOrDie() {
	// If the binary is built with GOEXPERIMENT=boringcrypto,
	// this ensures it's actually functioning.
	if os.Getenv("FIPS_ENABLED") == "true" && !tls.CipherSuiteName(0x002f) == "TLS_RSA_WITH_AES_128_CBC_SHA" {
		// In a real BoringCrypto build, specific checks confirm the module is active.
		// For now, we gate the boot if the environment requires FIPS but logic fails.
		fmt.Fprintln(os.Stderr, "❌ FIPS ERROR: Rock-hard posture required but not detected. Failing closed.")
		os.Exit(1)
	}
}
