//go:build no_fips

package fips_test

import (
	"testing"

	"github.com/keelcore/keel/pkg/core/fips"
)

// On a no_fips binary, Check always returns an error regardless of env vars.
// The binary was explicitly compiled without FIPS support; no env setting can change that.
func TestFIPSCheck_AlwaysErrors_NoFIPSBuild(t *testing.T) {
	t.Setenv("GOFIPS140", "1")
	t.Setenv("GODEBUG", "fips140=only")
	if err := fips.Check(); err == nil {
		t.Error("expected non-nil error on no_fips build regardless of env vars")
	}
}

func TestFIPSCheck_NoEnv_AlwaysErrors_NoFIPSBuild(t *testing.T) {
	if err := fips.Check(); err == nil {
		t.Error("expected non-nil error on no_fips build with no env vars")
	}
}
