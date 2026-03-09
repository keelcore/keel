package unit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// ---------------------------------------------------------------------------
// BuildTLSConfig — common assertions (both no_fips and !no_fips builds)
// ---------------------------------------------------------------------------

func TestBuildTLSConfig_MinVersionTLS13(t *testing.T) {
	cfg := keeltls.BuildTLSConfig(config.Config{})
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = 0x%04x, want 0x%04x (TLS 1.3)", cfg.MinVersion, tls.VersionTLS13)
	}
}

func TestBuildTLSConfig_NoMaxVersionCeiling(t *testing.T) {
	cfg := keeltls.BuildTLSConfig(config.Config{})
	if cfg.MaxVersion != 0 {
		t.Errorf("MaxVersion = 0x%04x, want 0 (no ceiling)", cfg.MaxVersion)
	}
}

// ---------------------------------------------------------------------------
// TLS 1.2 connection is rejected
// ---------------------------------------------------------------------------

func TestTLS12ConnectionRejected(t *testing.T) {
	certPEM, keyPEM := tlsTestCert(t, time.Now().Add(time.Hour))

	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	serverCfg := keeltls.BuildTLSConfig(config.Config{})
	serverCfg.Certificates = []tls.Certificate{serverCert}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Accept connections in background; ignore errors (handshake will fail).
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.(*tls.Conn).Handshake()
			}(conn)
		}
	}()

	clientCfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // test only
		MaxVersion:         tls.VersionTLS12,
	}
	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err == nil {
		conn.Close()
		t.Fatal("expected TLS 1.2 connection to be rejected, but handshake succeeded")
	}
}

// ---------------------------------------------------------------------------
// CertExpiry — gauge reads correct NotAfter from cert file
// ---------------------------------------------------------------------------

func TestCertExpiry_ReturnsNotAfter(t *testing.T) {
	notAfter := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	certPEM, _ := tlsTestCert(t, notAfter)

	f, err := os.CreateTemp(t.TempDir(), "keel-cert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(certPEM); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := keeltls.CertExpiry(f.Name())
	if err != nil {
		t.Fatalf("CertExpiry: %v", err)
	}
	if !got.Equal(notAfter) {
		t.Errorf("CertExpiry = %v, want %v", got, notAfter)
	}
}

func TestCertExpirySeconds_PositiveForFutureCert(t *testing.T) {
	certPEM, _ := tlsTestCert(t, time.Now().Add(time.Hour))

	f, err := os.CreateTemp(t.TempDir(), "keel-cert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(certPEM); err != nil {
		t.Fatal(err)
	}
	f.Close()

	secs, err := keeltls.CertExpirySeconds(f.Name())
	if err != nil {
		t.Fatalf("CertExpirySeconds: %v", err)
	}
	if secs <= 0 {
		t.Errorf("CertExpirySeconds = %f, want > 0 for a future cert", secs)
	}
}

// ---------------------------------------------------------------------------
// Helper: generate a minimal self-signed ECDSA cert.
// ---------------------------------------------------------------------------

func tlsTestCert(t *testing.T, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "keel-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}
