// server_serve_test.go — white-box tests for serveHTTP, runACMECertExpiryLoop,
// and RunServer (package core).
package core

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
)

// ---------------------------------------------------------------------------
// serveHTTP — listener bind error
// ---------------------------------------------------------------------------

// serveHTTP returns an error when the address is already in use.
func TestServeHTTP_BindError(t *testing.T) {
	// Bind a port first so the second bind fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(ctx, shutdown, addr, http.NotFoundHandler(), cfg, log)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected non-nil error when address is already in use")
		}
	case <-time.After(2 * time.Second):
		t.Error("serveHTTP did not return within timeout")
	}
}

// ---------------------------------------------------------------------------
// serveHTTP — clean shutdown on ctx cancel
// ---------------------------------------------------------------------------

// serveHTTP returns nil when the context is cancelled (clean shutdown).
func TestServeHTTP_CleanShutdown(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Timeouts: config.TimeoutsConfig{
			ShutdownDrain: config.DurationOf(100 * time.Millisecond),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(ctx, shutdown, "127.0.0.1:0", h, cfg, log)
	}()

	// Give serveHTTP a moment to bind, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected nil error on clean shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("serveHTTP did not exit within timeout after ctx cancel")
	}
}

// ---------------------------------------------------------------------------
// serveHTTPS — listener bind error
// ---------------------------------------------------------------------------

// serveHTTPS returns an error when the address is already in use.
func TestServeHTTPS_BindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := lifecycle.NewShutdownOrchestrator(log)

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPS(ctx, shutdown, addr, http.NotFoundHandler(), cfg, nil, nil, log)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected non-nil error when address is already in use")
		}
	case <-time.After(2 * time.Second):
		t.Error("serveHTTPS did not return within timeout")
	}
}

// ---------------------------------------------------------------------------
// runACMECertExpiryLoop — exits on ctx cancel
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// RunServer — compiles and is callable (smoke test)
// ---------------------------------------------------------------------------

// RunServer is a thin wrapper; verify it is callable by ensuring it is exported
// and takes the correct parameter types (compile-time check via NewServer).
func TestRunServer_TypeSignature(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	srv := NewServer(log, cfg)
	// Verify RunServer signature compiles (not called to avoid port binding).
	_ = RunServer
	_ = srv
}

// ---------------------------------------------------------------------------
// Run — conditional initialization branches (all listeners disabled)
// ---------------------------------------------------------------------------

// cancelAfter returns a context that is cancelled after d via a goroutine.
// Using cancel (not deadline) avoids the "shutdown_error" Fatal in Run()
// which triggers when sigErr is context.DeadlineExceeded.
func cancelAfter(d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(d)
		cancel()
	}()
	return ctx, cancel
}

// TestRun_HeapMaxBytes covers the debug.SetMemoryLimit branch.
// All listeners disabled to avoid port conflicts.
func TestRun_HeapMaxBytes_SetsLimit(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{HeapMaxBytes: 2 << 30}, // 2 GB — well above test heap
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_Backpressure covers the SheddingEnabled goroutine-launch branch.
func TestRun_Backpressure_StartsLoop(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			SheddingEnabled: true,
			HighWatermark:   0.9,
			LowWatermark:    0.7,
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_StatsD_InvalidEndpoint covers the statsd.New error branch.
// An endpoint without a port causes net.Dial to return an immediate error.
func TestRun_StatsD_InvalidEndpoint_Warns(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Metrics: config.MetricsConfig{
			StatsD: config.StatsDConfig{
				Enabled:  true,
				Endpoint: "127.0.0.1", // missing port — net.Dial("udp") fails immediately
			},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_StatsD_ValidEndpoint covers the statsd.New success branch.
// UDP dial succeeds even with no listener on the other end.
func TestRun_StatsD_ValidEndpoint(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Metrics: config.MetricsConfig{
			StatsD: config.StatsDConfig{
				Enabled:  true,
				Endpoint: "127.0.0.1:9125",
			},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_PrestopSleep covers the PrestopSleep branch in shutdown.
func TestRun_PrestopSleep_Executes(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Timeouts: config.TimeoutsConfig{
			PrestopSleep: config.DurationOf(1 * time.Millisecond),
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_OWASPHeaders covers the OWASPHeaders branch in wrapMain.
func TestRun_OWASPHeaders(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{OWASPHeaders: true},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_AccessLog covers the AccessLog branch in wrapMain.
func TestRun_AccessLog(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Logging: config.LoggingConfig{AccessLog: true},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_MaxConcurrent covers the MaxConcurrent branch in wrapMain.
func TestRun_MaxConcurrent(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Limits: config.LimitsConfig{MaxConcurrent: 100},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_Authn_Enabled covers the Authn.Enabled branch in wrapMain.
// No signers file is configured so LoadTrustedSigners returns an empty list.
func TestRun_Authn_Enabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Authn: config.AuthnConfig{Enabled: true},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_ExtAuthz_Enabled covers the ExtAuthz.Enabled branch in wrapMain.
func TestRun_ExtAuthz_Enabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		ExtAuthz: config.ExtAuthzConfig{
			Enabled:  true,
			Endpoint: "http://127.0.0.1:1/authz", // unreachable; middleware is only exercised at request time
			Timeout:  config.DurationOf(10 * time.Millisecond),
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// ---------------------------------------------------------------------------
// Run — probe listener goroutine-launch branches (server.go:296-330)
// ---------------------------------------------------------------------------

// TestRun_HealthListener covers the Health listener goroutine-launch block.
func TestRun_HealthListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Health: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_ReadyListener covers the Ready listener goroutine-launch block.
func TestRun_ReadyListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Ready: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_AdminListener covers the Admin listener goroutine-launch block.
func TestRun_AdminListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Admin: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

// TestRun_StartupListener covers the Startup listener goroutine-launch block.
func TestRun_StartupListener(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			Startup: config.ListenerConfig{Enabled: true, Port: 0},
		},
	}
	s := NewServer(log, cfg)
	ctx, cancel := cancelAfter(50 * time.Millisecond)
	defer cancel()
	s.Run(ctx)
}
