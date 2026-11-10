package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// runCertExpiryLoop: exits immediately when ctx is already cancelled.
// A non-existent cert file causes CertExpirySeconds to error (skipped silently),
// then the ticker select immediately resolves ctx.Done.
func TestRunCertExpiryLoop_ExitsOnCancel(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runCertExpiryLoop(ctx, "/nonexistent-cert-for-test.pem", met)
}

// runLogDropsLoop: exits immediately when ctx is already cancelled.
func TestRunLogDropsLoop_ExitsOnCancel(t *testing.T) {
	met := metrics.New()
	sink := logging.NewHTTPSink("http://127.0.0.1:1", 10, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runLogDropsLoop(ctx, func() *logging.HTTPSink { return sink }, met)
}

// runFIPSMonitorLoop: exits immediately when ctx is already cancelled.
func TestRunFIPSMonitorLoop_ExitsOnCancel(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runFIPSMonitorLoop(ctx, met)
}

func TestTickACMECertExpiry_Success(t *testing.T) {
	orig := acmeCertExpiryFn
	acmeCertExpiryFn = func(*acme.Manager) (float64, error) { return 4200, nil }
	t.Cleanup(func() { acmeCertExpiryFn = orig })

	tickACMECertExpiry(acme.New(), metrics.New()) // must call SetCertExpiry
}

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

func TestTickACMECertExpiry_NoCert(t *testing.T) {
	met := metrics.New()
	mgr := acme.New() // no cert stored → CertExpiry errors → SetCertExpiry not called
	// Verify no panic and no side effect.
	tickACMECertExpiry(mgr, met)
}

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

func TestTickFIPSMonitor_NoFIPS(t *testing.T) {
	met := metrics.New()
	// In a non-FIPS build keelfips.Check() returns nil → IncFIPSMonitorFailure not called.
	// Just verify no panic.
	tickFIPSMonitor(met)
}

// runCertExpiryLoop: ticks at least once before ctx cancellation.
// Uses a very short ticker interval via the underlying time.Ticker default of 1h;
// we just verify the cancel path exits cleanly even if a tick never fires.
func TestRunCertExpiryLoop_CancelAfterStart(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Non-existent cert file: CertExpirySeconds errors silently.
	runCertExpiryLoop(ctx, "/nonexistent-cert-loop-test.pem", met)
}

// runLogDropsLoop: exits cleanly when ctx expires; nil getSink is not called.
func TestRunLogDropsLoop_NilSinkNoBlock(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runLogDropsLoop(ctx, func() *logging.HTTPSink { return nil }, met)
}

// runFIPSMonitorLoop: exits when ctx is cancelled.
func TestRunFIPSMonitorLoop_CancelAfterStart(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runFIPSMonitorLoop(ctx, met)
}

// startBackgroundLoops with SheddingEnabled starts the pressure loop goroutine.
func TestStartBackgroundLoops_SheddingEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = true
	cfg.Listeners.HTTPS.Enabled = false
	cfg.FIPS.Monitor = false
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so goroutines exit

	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, nil)
	wg.Wait()
}

// startBackgroundLoops with FIPS.Monitor=true starts the FIPS monitor goroutine.
func TestStartBackgroundLoops_FIPSMonitor(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = false
	cfg.Listeners.HTTPS.Enabled = false
	cfg.FIPS.Monitor = true
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	met := metrics.New()
	s.met = met

	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, nil)
	wg.Wait()
}

// startBackgroundLoops with HTTPS+ACME enabled starts the ACME cert expiry loop.
func TestStartBackgroundLoops_HTTPSWithACME(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = false
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = true
	cfg.FIPS.Monitor = false
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	acmeMgr := s.newACMEManager()
	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, acmeMgr)
	wg.Wait()
}

// startBackgroundLoops with HTTPS enabled and certLoader set starts the file cert expiry loop.
func TestStartBackgroundLoops_HTTPSWithCertLoader(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	certPath, keyPath := writeTempCertKeyPair(t)
	cfg := config.Defaults()
	cfg.Backpressure.SheddingEnabled = false
	cfg.Listeners.HTTPS.Enabled = true
	cfg.TLS.ACME.Enabled = false
	cfg.TLS.CertFile = certPath
	cfg.FIPS.Monitor = false
	s := NewServer(log, cfg)

	loader, err := keeltls.NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	s.certLoader = loader

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	s.startBackgroundLoops(ctx, &wg, nil)
	wg.Wait()
}

// runACMECertExpiryLoop exits immediately when ctx is already cancelled.
// Uses a real acme.Manager with no cert stored (CertExpiry errors silently).
func TestRunACMECertExpiryLoop_ExitsOnCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	met := metrics.New()
	mgr := acme.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Should return immediately: ctx is already done.
	runACMECertExpiryLoop(ctx, mgr, met)
}
