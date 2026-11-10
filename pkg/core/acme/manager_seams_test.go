//go:build !no_acme

package acme

import (
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

// Seam-injection tests that exercise the otherwise-unreachable library-error
// and timing branches of manager.go without contacting a real ACME CA. Each
// test overrides a package-level seam (genKey, afterFunc, marshalECKey,
// systemCertPool, buildClient) or crafts malformed cert bytes, then restores
// the seam via t.Cleanup. Reuses helpers defined in the sibling test files
// (writeCachedCert, stubACMEClient, makeECDSACertDER) — same package.

// ---------------------------------------------------------------------------
// CertExpiry — parse-error branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// setupACMEClient / loadOrCreateAccountKey — key-generation error branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// waitForRenewalWindow — timer-fired (return true) branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Validate — trivial pass
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// registerAccount — HTTP 409 conflict (already-registered) branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Start — register-error, fresh-cert return, and renewal-loop branches
//
// All three override the buildClient seam to inject a stub acmeClient so Start
// never contacts a CA.
// ---------------------------------------------------------------------------

func withStubClient(t *testing.T, stub acmeClient) {
	t.Helper()
	orig := buildClient
	buildClient = func(*Manager, config.ACMEConfig) (acmeClient, error) { return stub, nil }
	t.Cleanup(func() { buildClient = orig })
}

// ---------------------------------------------------------------------------
// storeCert — writeCertPEM error after MkdirAll succeeds
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// assembleTLSCert / writeKeyPEM — MarshalECPrivateKey error branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// renewalDelay / validateCert — malformed leaf-bytes branches
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// httpClientWithCA — SystemCertPool error → NewCertPool fallback
// ---------------------------------------------------------------------------
