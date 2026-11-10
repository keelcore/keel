// server_helpers_test.go — unit tests for the helper functions extracted from server.go.
package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// ---------------------------------------------------------------------------
// runTickLoop
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tickACMECertExpiry
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tickFileCertExpiry
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tickLogDrops
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tickFIPSMonitor
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildHealthMux
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildReadyMux
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildStartupMux
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildAdminMux
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// newHTTPServer
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// applyTLSCertSource
// ---------------------------------------------------------------------------

func TestApplyTLSCertSource_LoaderTakesPrecedence(t *testing.T) {
	certPath, keyPath := writeTempCertKeyPair(t)
	loader, err := keeltls.NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	tlsCfg := &tls.Config{}
	// sentinel must NOT be selected when loader is non-nil.
	var sentinelCalled bool
	sentinel := func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		sentinelCalled = true
		return nil, nil
	}
	applyTLSCertSource(tlsCfg, loader, sentinel)
	if tlsCfg.GetCertificate == nil {
		t.Fatal("expected GetCertificate to be set")
	}
	// Call it; loader.Get should be invoked, not sentinel.
	_, _ = tlsCfg.GetCertificate(nil)
	if sentinelCalled {
		t.Error("sentinel getCert should not be called when loader is non-nil")
	}
}

func TestApplyTLSCertSource_FallsBackToGetCert(t *testing.T) {
	tlsCfg := &tls.Config{}
	var called bool
	getCert := func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		called = true
		return nil, nil
	}
	applyTLSCertSource(tlsCfg, nil, getCert)
	if tlsCfg.GetCertificate == nil {
		t.Fatal("expected GetCertificate to be set")
	}
	_, _ = tlsCfg.GetCertificate(nil)
	if !called {
		t.Error("expected getCert to be invoked via GetCertificate")
	}
}

// TestApplyTLSCertSource_LoaderGetCertificate verifies that a loader's Get is set
// when a matching cert+key pair is available.
func TestApplyTLSCertSource_LoaderGetCertificate(t *testing.T) {
	certPath, keyPath := writeTempCertKeyPair(t)
	loader, err := keeltls.NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	tlsCfg := &tls.Config{}
	applyTLSCertSource(tlsCfg, loader, nil)
	if tlsCfg.GetCertificate == nil {
		t.Error("expected GetCertificate to be set from loader")
	}
}

// writeTempCertKeyPair writes a matching self-signed cert+key pair to temp files.
func writeTempCertKeyPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	certPath = filepath.Join(dir, "cert.pem")
	cf, err := os.Create(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	_ = cf.Close()

	keyPath = filepath.Join(dir, "key.pem")
	kf, err := os.Create(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	_ = kf.Close()
	return certPath, keyPath
}

// ---------------------------------------------------------------------------
// defaultKeeHandler
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// newSignFn
// ---------------------------------------------------------------------------
