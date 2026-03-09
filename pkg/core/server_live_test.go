package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/statsd"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// generateTestCert writes a self-signed ECDSA cert and key to temp files and
// returns their paths.  Files live inside t.TempDir() and are removed when the
// test ends.
func generateTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(cryptorand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	certF, err := os.CreateTemp(dir, "cert*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(certF, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatal(err)
	}
	certF.Close()

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyF, err := os.CreateTemp(dir, "key*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(keyF, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	keyF.Close()

	return certF.Name(), keyF.Name()
}

// shortDrainCfg returns a Config with a 1 ms shutdown drain so tests that
// pre-cancel the context complete in microseconds instead of the default 10 s.
func shortDrainCfg() config.Config {
	cfg := config.Config{}
	cfg.Timeouts.ShutdownDrain = config.DurationOf(time.Millisecond)
	return cfg
}

// TestServeHTTPS_CancelledContext: the TLS listener binds on a random port,
// the context is pre-cancelled, serveHTTPS shuts down cleanly and returns nil.
func TestServeHTTPS_CancelledContext(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := serveHTTPS(ctx, shutdown, "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), loader, log); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestServeHTTPS_ListenError: an invalid address causes net.Listen to fail;
// serveHTTPS returns an error without blocking.
func TestServeHTTPS_ListenError(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	log := logging.New(logging.Config{Out: io.Discard})
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	err = serveHTTPS(context.Background(), shutdown, "256.0.0.1:0",
		http.NotFoundHandler(), config.Config{}, loader, log)
	if err == nil {
		t.Fatal("expected error from invalid address, got nil")
	}
}

// TestServeH3_CertMissing: ListenAndServeTLS fails immediately when cert files
// do not exist; serveH3 returns the error via the errCh select branch.
func TestServeH3_CertMissing(t *testing.T) {
	cfg := config.Config{}
	cfg.TLS.CertFile = "/nonexistent-cert-for-test.pem"
	cfg.TLS.KeyFile = "/nonexistent-key-for-test.pem"

	err := serveH3(context.Background(), "127.0.0.1:18443",
		http.NotFoundHandler(), cfg, logging.New(logging.Config{Out: io.Discard}))
	if err == nil {
		t.Fatal("expected error from missing cert files, got nil")
	}
}

// TestWrapMain_Flags exercises each conditional middleware branch in wrapMain
// by enabling one flag at a time and verifying no panic occurs.
func TestWrapMain_Flags(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})

	cases := []struct {
		name string
		cfg  config.Config
	}{
		{
			name: "owasp",
			cfg: func() config.Config {
				c := config.Config{}
				c.Security.OWASPHeaders = true
				return c
			}(),
		},
		{
			name: "shedding",
			cfg: func() config.Config {
				c := config.Config{}
				c.Backpressure.SheddingEnabled = true
				return c
			}(),
		},
		{
			name: "accesslog",
			cfg: func() config.Config {
				c := config.Config{}
				c.Logging.AccessLog = true
				return c
			}(),
		},
		{
			name: "maxconcurrent",
			cfg: func() config.Config {
				c := config.Config{}
				c.Limits.MaxConcurrent = 10
				return c
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(log, tc.cfg)
			h := s.wrapMain(http.NotFoundHandler())
			if h == nil {
				t.Fatal("expected non-nil handler")
			}
		})
	}
}

// TestWrapMain_StatsDInstrumented exercises the s.sd != nil branch by
// injecting a statsd client directly (UDP; nothing need be listening).
func TestWrapMain_StatsDInstrumented(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	sd, err := statsd.New("127.0.0.1:8125", "keel")
	if err != nil {
		t.Skipf("cannot dial UDP for statsd test: %v", err)
	}
	s.sd = sd

	h := s.wrapMain(http.NotFoundHandler())
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}
