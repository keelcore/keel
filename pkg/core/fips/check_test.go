//go:build !no_fips

package fips

import (
	"crypto/fips140"
	"testing"
)

func stubFIPS(t *testing.T, enabled bool) {
	t.Helper()
	orig := fipsEnabled
	fipsEnabled = func() bool { return enabled }
	t.Cleanup(func() { fipsEnabled = orig })
}

// Check passes only when the crypto module reports FIPS 140 mode.
func TestFIPSCheck_EnabledReturnsNil(t *testing.T) {
	stubFIPS(t, true)
	if err := Check(); err != nil {
		t.Errorf("expected nil with FIPS mode active, got %v", err)
	}
}

func TestFIPSCheck_DisabledReturnsError(t *testing.T) {
	stubFIPS(t, false)
	if err := Check(); err == nil {
		t.Error("expected error with FIPS mode inactive")
	}
}

// Environment variables cannot fake FIPS: GOFIPS140=off (non-empty) and a
// runtime GODEBUG change do not enable it, and Check must not believe them.
func TestFIPSCheck_EnvCannotFakeFIPS(t *testing.T) {
	stubFIPS(t, false)
	t.Setenv("GOFIPS140", "off")
	t.Setenv("GODEBUG", "fips140=only")
	if err := Check(); err == nil {
		t.Error("Check trusted environment variables instead of the crypto module")
	}
}

// Unstubbed, Check agrees with the real crypto module in this test binary
// (inactive in a plain test run; active under make max's GODEBUG=fips140=only).
func TestFIPSCheck_MatchesCryptoModule(t *testing.T) {
	if err := Check(); (err == nil) != fips140.Enabled() {
		t.Errorf("Check() = %v but fips140.Enabled() = %v", err, fips140.Enabled())
	}
}
