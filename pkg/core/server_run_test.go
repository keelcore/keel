// server_run_test.go — unit tests for Run helpers extracted from server.go.
package core

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// ---------------------------------------------------------------------------
// newACMEManager
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildMainRouter — registrar invocation
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// newSignFn — non-nil signer path
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// RunServer
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// drainListeners — additional branches
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// startBackgroundLoops — additional branches
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// registerDefaultRoutes — H3 port
// ---------------------------------------------------------------------------
