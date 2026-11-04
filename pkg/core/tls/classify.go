package tls

import (
	"crypto/tls"
	"slices"

	"github.com/keelcore/keel/pkg/core/protonotice"
)

// defaultGroups are the key exchange groups Go offers when a policy sets no CurvePreferences.
var defaultGroups = []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521}

// tls13Suites are the TLS 1.3 cipher suites; under FIPS 140 only the AES-GCM ones are usable.
var (
	tls13Suites     = []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256}
	tls13FIPSSuites = []uint16{tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384}
)

// Classify compares a ClientHello with the listener policy keel built and returns
// the code of the first requirement the client cannot meet, or "" when the policy
// accepts it. It reads only the policy, never fixed constants, so a protocol the
// configuration allows always classifies as accepted.
func Classify(hello *tls.ClientHelloInfo, policy *tls.Config) protonotice.Code {
	if maxVersion(hello.SupportedVersions) < policy.MinVersion {
		return protonotice.TLSVersion
	}
	if len(hello.SupportedProtos) > 0 && len(policy.NextProtos) > 0 && !shares(hello.SupportedProtos, policy.NextProtos) {
		return protonotice.NoSharedALPN
	}
	groups := policy.CurvePreferences
	if len(groups) == 0 {
		groups = defaultGroups
	}
	if !shares(hello.SupportedCurves, groups) {
		return protonotice.NoSharedGroup
	}
	if policy.MinVersion >= tls.VersionTLS13 {
		suites := tls13Suites
		if len(policy.CurvePreferences) > 0 && !slices.Contains(policy.CurvePreferences, tls.X25519) {
			suites = tls13FIPSSuites
		}
		if !shares(hello.CipherSuites, suites) {
			return protonotice.NoSharedCipher
		}
	}
	return ""
}

// ObserveRejections makes cfg report every ClientHello that Classify rejects to
// observe, before the handshake fails as it always would. Accepted hellos
// continue with cfg unchanged.
func ObserveRejections(cfg *tls.Config, observe func(protonotice.Code, *tls.ClientHelloInfo)) {
	policy := cfg.Clone()
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if code := Classify(hello, policy); code != "" {
			observe(code, hello)
		}
		return nil, nil
	}
}

func maxVersion(versions []uint16) uint16 {
	if len(versions) == 0 {
		return 0
	}
	return slices.Max(versions)
}

func shares[T comparable](a, b []T) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
