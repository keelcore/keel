//go:build !no_fips

package fips_test

import (
	"os"
	"testing"

	"github.com/keelcore/keel/pkg/core/fips"
)

// clearFIPSEnv removes both FIPS-signalling env vars and restores them after t.
func clearFIPSEnv(t *testing.T) {
	t.Helper()
	orig1, ok1 := os.LookupEnv("GOFIPS140")
	orig2, ok2 := os.LookupEnv("GODEBUG")
	os.Unsetenv("GOFIPS140")
	os.Unsetenv("GODEBUG")
	t.Cleanup(func() {
		if ok1 {
			os.Setenv("GOFIPS140", orig1)
		} else {
			os.Unsetenv("GOFIPS140")
		}
		if ok2 {
			os.Setenv("GODEBUG", orig2)
		} else {
			os.Unsetenv("GODEBUG")
		}
	})
}

// Check returns nil when GOFIPS140 is set to a non-empty value.
func TestFIPSCheck_GOFIPS140_ReturnsNil(t *testing.T) {
	clearFIPSEnv(t)
	t.Setenv("GOFIPS140", "1")
	if err := fips.Check(); err != nil {
		t.Errorf("expected nil with GOFIPS140=1, got %v", err)
	}
}

// Check returns nil when GODEBUG contains fips140=only.
func TestFIPSCheck_GODEBUGFips140Only_ReturnsNil(t *testing.T) {
	clearFIPSEnv(t)
	t.Setenv("GODEBUG", "fips140=only")
	if err := fips.Check(); err != nil {
		t.Errorf("expected nil with GODEBUG=fips140=only, got %v", err)
	}
}

// Check returns nil when GODEBUG contains fips140=only alongside other settings.
func TestFIPSCheck_GODEBUGComposite_ReturnsNil(t *testing.T) {
	clearFIPSEnv(t)
	t.Setenv("GODEBUG", "netdns=go,fips140=only,asyncpreemptoff=1")
	if err := fips.Check(); err != nil {
		t.Errorf("expected nil with composite GODEBUG containing fips140=only, got %v", err)
	}
}

// Check returns a non-nil error when neither FIPS env var is present.
func TestFIPSCheck_NoEnv_ReturnsError(t *testing.T) {
	clearFIPSEnv(t)
	if err := fips.Check(); err == nil {
		t.Error("expected non-nil error when no FIPS env vars are set")
	}
}

// Check returns a non-nil error when GOFIPS140 is empty string.
func TestFIPSCheck_EmptyGOFIPS140_ReturnsError(t *testing.T) {
	clearFIPSEnv(t)
	t.Setenv("GOFIPS140", "")
	if err := fips.Check(); err == nil {
		t.Error("expected non-nil error when GOFIPS140 is empty string")
	}
}

// Check returns a non-nil error when GODEBUG does not contain fips140=only.
func TestFIPSCheck_GODEBUGWithoutFips_ReturnsError(t *testing.T) {
	clearFIPSEnv(t)
	t.Setenv("GODEBUG", "netdns=go")
	if err := fips.Check(); err == nil {
		t.Error("expected non-nil error when GODEBUG does not contain fips140=only")
	}
}
