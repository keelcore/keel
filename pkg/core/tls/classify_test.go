package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

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

// A hello that lists no versions at all is below any floor.
func TestClassify_NoVersions(t *testing.T) {
	h := modern()
	h.SupportedVersions = nil
	if got := Classify(h, standardPolicy); got != protonotice.TLSVersion {
		t.Fatalf("Classify = %q, want %q", got, protonotice.TLSVersion)
	}
}

// handshake runs one TLS handshake over an in-process pipe and returns the codes
// the server's observer saw and the server's handshake error.
func handshake(t *testing.T, policy *tls.Config, client *tls.Config) ([]protonotice.Code, error) {
	t.Helper()
	cert := testCert(t)
	server := policy.Clone()
	server.Certificates = []tls.Certificate{cert}
	var mu sync.Mutex
	var seen []protonotice.Code
	ObserveRejections(server, func(code protonotice.Code, _ *tls.ClientHelloInfo) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, code)
	})
	sc, cc := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- tls.Server(sc, server).Handshake() }()
	c := tls.Client(cc, client)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_ = c.Handshake()
	serr := <-done
	// Close the pipe itself: a TLS close_notify from both ends would block on the unbuffered pipe.
	_ = cc.Close()
	_ = sc.Close()
	mu.Lock()
	defer mu.Unlock()
	return seen, serr
}

// U1/U2: a rejected handshake is reported with its code; the handshake still fails.
func TestObserveRejections_ReportsTheCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy *tls.Config
		client *tls.Config
		want   protonotice.Code
	}{
		{"TLS 1.2 client", standardPolicy, &tls.Config{MaxVersion: tls.VersionTLS12, ServerName: "test"}, protonotice.TLSVersion},
		{"no shared ALPN", standardPolicy, &tls.Config{NextProtos: []string{"foo"}, ServerName: "test"}, protonotice.NoSharedALPN},
		{"P-521 only vs FIPS curves", fipsPolicy, &tls.Config{CurvePreferences: []tls.CurveID{tls.CurveP521}, ServerName: "test"}, protonotice.NoSharedGroup},
	} {
		seen, err := handshake(t, tc.policy, tc.client)
		if err == nil {
			t.Errorf("%s: handshake succeeded, want it refused", tc.name)
		}
		if len(seen) != 1 || seen[0] != tc.want {
			t.Errorf("%s: observed %v, want [%s]", tc.name, seen, tc.want)
		}
	}
}

// An accepted handshake is never reported.
func TestObserveRejections_SilentForAllowedClients(t *testing.T) {
	seen, err := handshake(t, standardPolicy, &tls.Config{RootCAs: testRoots(t), ServerName: "test", NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("observed %v for an allowed client", seen)
	}
}

var (
	sanOnce sync.Once
	sanCert tls.Certificate
	sanPool *x509.CertPool
)

// testCert is a self-signed P-256 certificate for "test", usable under FIPS 140.
func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	sanOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "test"},
			DNSNames:              []string{"test"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		sanCert = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
		sanPool = x509.NewCertPool()
		sanPool.AddCert(leaf)
	})
	return sanCert
}

// testRoots trusts testCert.
func testRoots(t *testing.T) *x509.CertPool {
	t.Helper()
	testCert(t)
	return sanPool
}
