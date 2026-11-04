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
