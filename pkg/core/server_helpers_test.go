// server_helpers_test.go — unit tests for the helper functions extracted from server.go.
package core

import (
	"context"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/acme"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// ---------------------------------------------------------------------------
// runTickLoop
// ---------------------------------------------------------------------------

func TestRunTickLoop_CallsFnImmediately(t *testing.T) {
	called := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled: only the immediate call should fire
	runTickLoop(ctx, time.Hour, func() {
		called <- struct{}{}
	})
	if len(called) < 1 {
		t.Error("expected fn to be called at least once immediately")
	}
}

func TestRunTickLoop_CallsFnOnTick(t *testing.T) {
	var count atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	runTickLoop(ctx, 1*time.Millisecond, func() {
		count.Add(1)
	})
	if count.Load() < 2 {
		t.Errorf("expected fn called more than once, got %d", count.Load())
	}
}

func TestRunTickLoop_ExitsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runTickLoop(ctx, time.Hour, func() {})
		close(done)
	}()
	select {
	case <-done:
		// good
	case <-time.After(500 * time.Millisecond):
		t.Error("runTickLoop did not exit promptly on pre-cancelled ctx")
	}
}

// ---------------------------------------------------------------------------
// tickACMECertExpiry
// ---------------------------------------------------------------------------

func TestTickACMECertExpiry_NoCert(t *testing.T) {
	met := metrics.New()
	mgr := acme.New() // no cert stored → CertExpiry errors → SetCertExpiry not called
	// Verify no panic and no side effect.
	tickACMECertExpiry(mgr, met)
}

// ---------------------------------------------------------------------------
// tickFileCertExpiry
// ---------------------------------------------------------------------------

func TestTickFileCertExpiry_MissingFile(t *testing.T) {
	met := metrics.New()
	// Non-existent file: CertExpirySeconds errors → SetCertExpiry not called.
	tickFileCertExpiry("/nonexistent-cert-for-helpers-test.pem", met)
}

func TestTickFileCertExpiry_ValidFile(t *testing.T) {
	// Write a self-signed certificate to a temp file.
	certPath := writeTempSelfSignedCert(t)

	met := metrics.New()
	// Should succeed without panic.
	tickFileCertExpiry(certPath, met)
}

// writeTempSelfSignedCert creates a temporary PEM cert file and returns its path.
func writeTempSelfSignedCert(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

// ---------------------------------------------------------------------------
// tickLogDrops
// ---------------------------------------------------------------------------

func TestTickLogDrops_NilSink(t *testing.T) {
	met := metrics.New()
	// getSink returns nil: no panic, metric untouched.
	tickLogDrops(func() *logging.HTTPSink { return nil }, met)
}

func TestTickLogDrops_WithSink(t *testing.T) {
	met := metrics.New()
	sink := logging.NewHTTPSink("http://127.0.0.1:1/ingest", 10, time.Hour)
	// drops = 0 initially; SetLogDrops(0) should not panic.
	tickLogDrops(func() *logging.HTTPSink { return sink }, met)
}

// ---------------------------------------------------------------------------
// tickFIPSMonitor
// ---------------------------------------------------------------------------

func TestTickFIPSMonitor_NoFIPS(t *testing.T) {
	met := metrics.New()
	// In a non-FIPS build keelfips.Check() returns nil → IncFIPSMonitorFailure not called.
	// Just verify no panic.
	tickFIPSMonitor(met)
}

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
