// pkg/core/server_loops.go
package core

import (
	"context"
	"sync"
	"time"

	"github.com/keelcore/keel/pkg/core/acme"
	keelfips "github.com/keelcore/keel/pkg/core/fips"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// startBackgroundLoops starts the metric and monitor goroutines that run for
// the lifetime of the server.
func (s *Server) startBackgroundLoops(ctx context.Context, wg *sync.WaitGroup, acmeMgr *acme.Manager) {
	// Goroutines below use this snapshot, never s.cfg: a reload must not race them.
	cfg := s.cfg
	if s.cfg.Listeners.HTTPS.Enabled {
		if s.cfg.TLS.ACME.Enabled {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runACMECertExpiryLoop(ctx, acmeMgr, s.met)
			}()
		} else if s.certLoader != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runCertExpiryLoop(ctx, cfg.TLS.CertFile, s.met)
			}()
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		runLogDropsLoop(ctx, s.httpSink.Load, s.met)
	}()
	if s.cfg.FIPS.Monitor {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runFIPSMonitorLoop(ctx, s.met)
		}()
	}
}

// runTickLoop runs fn immediately then on every tick until ctx is done.
func runTickLoop(ctx context.Context, interval time.Duration, fn func()) {
	fn()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

func tickACMECertExpiry(mgr *acme.Manager, met *metrics.Metrics) {
	if secs, err := acmeCertExpiryFn(mgr); err == nil {
		met.SetCertExpiry(secs)
	}
}

func tickFileCertExpiry(certFile string, met *metrics.Metrics) {
	if secs, err := keeltls.CertExpirySeconds(certFile); err == nil {
		met.SetCertExpiry(secs)
	}
}

func tickLogDrops(getSink func() *logging.HTTPSink, met *metrics.Metrics) {
	if sink := getSink(); sink != nil {
		met.SetLogDrops(sink.DropsTotal())
	}
}

func tickFIPSMonitor(met *metrics.Metrics) {
	if err := keelfips.Check(); err != nil {
		met.IncFIPSMonitorFailure()
	}
}

func runACMECertExpiryLoop(ctx context.Context, mgr *acme.Manager, met *metrics.Metrics) {
	runTickLoop(ctx, time.Hour, func() { tickACMECertExpiry(mgr, met) })
}

func runCertExpiryLoop(ctx context.Context, certFile string, met *metrics.Metrics) {
	runTickLoop(ctx, time.Hour, func() { tickFileCertExpiry(certFile, met) })
}

func runLogDropsLoop(ctx context.Context, getSink func() *logging.HTTPSink, met *metrics.Metrics) {
	runTickLoop(ctx, 30*time.Second, func() { tickLogDrops(getSink, met) })
}

func runFIPSMonitorLoop(ctx context.Context, met *metrics.Metrics) {
	runTickLoop(ctx, time.Hour, func() { tickFIPSMonitor(met) })
}
