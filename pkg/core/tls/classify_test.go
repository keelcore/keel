package tls

import (
	"crypto/tls"
	"testing"

	"github.com/keelcore/keel/pkg/core/protonotice"
)

var (
	// standardPolicy is keel's non-FIPS listener policy: TLS 1.3, h2 + http/1.1, Go's default groups.
	standardPolicy = &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}
	// fipsPolicy restricts key exchange to the FIPS curves.
	fipsPolicy = &tls.Config{
		MinVersion:       tls.VersionTLS13,
		NextProtos:       []string{"h2", "http/1.1"},
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.CurveP384},
	}
)

// modern is a hello every keel policy accepts.
func modern() *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedCurves:   []tls.CurveID{tls.X25519, tls.CurveP256},
		CipherSuites:      []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_CHACHA20_POLY1305_SHA256},
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*tls.ClientHelloInfo)
		policy *tls.Config
		want   protonotice.Code
	}{
		{"modern, standard", func(*tls.ClientHelloInfo) {}, standardPolicy, ""},
		{"modern, fips", func(*tls.ClientHelloInfo) {}, fipsPolicy, ""},
		{"TLS 1.2 max", func(h *tls.ClientHelloInfo) { h.SupportedVersions = []uint16{tls.VersionTLS12, tls.VersionTLS11} }, standardPolicy, protonotice.TLSVersion},
		{"TLS 1.0 only", func(h *tls.ClientHelloInfo) { h.SupportedVersions = []uint16{tls.VersionTLS10} }, fipsPolicy, protonotice.TLSVersion},
		{"no ALPN offered", func(h *tls.ClientHelloInfo) { h.SupportedProtos = nil }, standardPolicy, ""},
		{"only unknown ALPN", func(h *tls.ClientHelloInfo) { h.SupportedProtos = []string{"foo", "spdy/3"} }, standardPolicy, protonotice.NoSharedALPN},
		{"X25519 only, standard", func(h *tls.ClientHelloInfo) { h.SupportedCurves = []tls.CurveID{tls.X25519} }, standardPolicy, ""},
		{"X25519 only, fips", func(h *tls.ClientHelloInfo) { h.SupportedCurves = []tls.CurveID{tls.X25519} }, fipsPolicy, protonotice.NoSharedGroup},
		{"P-521 only, standard", func(h *tls.ClientHelloInfo) { h.SupportedCurves = []tls.CurveID{tls.CurveP521} }, standardPolicy, ""},
		{"ChaCha20 only, standard", func(h *tls.ClientHelloInfo) { h.CipherSuites = []uint16{tls.TLS_CHACHA20_POLY1305_SHA256} }, standardPolicy, ""},
		{"ChaCha20 only, fips", func(h *tls.ClientHelloInfo) { h.CipherSuites = []uint16{tls.TLS_CHACHA20_POLY1305_SHA256} }, fipsPolicy, protonotice.NoSharedCipher},
		{"no TLS 1.3 suite", func(h *tls.ClientHelloInfo) {
			h.CipherSuites = []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}
		}, standardPolicy, protonotice.NoSharedCipher},
		{"version is checked first", func(h *tls.ClientHelloInfo) {
			h.SupportedVersions = []uint16{tls.VersionTLS12}
			h.SupportedProtos = []string{"foo"}
		}, standardPolicy, protonotice.TLSVersion},
	} {
		h := modern()
		tc.edit(h)
		if got := Classify(h, tc.policy); got != tc.want {
			t.Errorf("%s: Classify = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The governing rule: a protocol the policy allows classifies as ok. A policy
// that serves only http/1.1 rejects an h2-only client, and one that allows
// TLS 1.2 accepts a TLS 1.2 client.
func TestClassify_FollowsThePolicyNotConstants(t *testing.T) {
	h1only := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}}
	h := modern()
	h.SupportedProtos = []string{"h2"}
	if got := Classify(h, h1only); got != protonotice.NoSharedALPN {
		t.Errorf("h2-only client, http/1.1 policy: %q, want %q", got, protonotice.NoSharedALPN)
	}
	tls12 := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	h = modern()
	h.SupportedVersions = []uint16{tls.VersionTLS12}
	h.CipherSuites = []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}
	if got := Classify(h, tls12); got != "" {
		t.Errorf("TLS 1.2 client, TLS 1.2 policy: %q, want ok", got)
	}
}
