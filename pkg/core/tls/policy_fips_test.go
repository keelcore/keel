//go:build !no_fips

package tls

import (
	"crypto/tls"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

func TestBuildTLSConfigFIPS(t *testing.T) {
	cfg := BuildTLSConfig(config.Config{})
	if cfg == nil {
		t.Fatal("BuildTLSConfig returned nil")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %d, want %d", cfg.MinVersion, tls.VersionTLS13)
	}
	want := []tls.CurveID{tls.CurveP256, tls.CurveP384}
	if len(cfg.CurvePreferences) != len(want) {
		t.Fatalf("CurvePreferences = %v, want %v", cfg.CurvePreferences, want)
	}
	for i, c := range want {
		if cfg.CurvePreferences[i] != c {
			t.Fatalf("CurvePreferences[%d] = %v, want %v", i, cfg.CurvePreferences[i], c)
		}
	}
}
