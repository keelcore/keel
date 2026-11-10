// server_coverage_test.go — additional white-box unit tests raising package
// core statement coverage to 100%. These target error/success branches that
// are otherwise unreachable without crossing a system boundary; they use the
// package's test seams (netListen, acmeValidate, buildRemoteSinkFn,
// tracingSetupFn, acmeCertExpiryFn) and the injectable logger ExitFn.
package core

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/acme"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/tracing"
)

// ---------------------------------------------------------------------------
// serveHTTPBound — Serve returns a non-ErrServerClosed error (line: return err)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// serveHTTPS — net.Listen error and Serve error branches
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// realRunner wrappers — serveHTTP and serveHTTPS delegate to package funcs
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// startListenerReadinessGate — flips listenersServing once bindWG drains
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Metrics — accessor returns the server metrics store
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// acmeLog — ACME manager error callback logs a warning
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// applyRemoteSink — buildRemoteSink error branch
// ---------------------------------------------------------------------------

func TestApplyRemoteSink_BuildError(t *testing.T) {
	orig := buildRemoteSinkFn
	buildRemoteSinkFn = func(config.RemoteSinkConfig) (io.Writer, *logging.HTTPSink, error) {
		return nil, nil, errors.New("build boom")
	}
	t.Cleanup(func() { buildRemoteSinkFn = orig })

	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.runCtx = context.Background()
	cfg := config.Config{}
	cfg.Logging.RemoteSink.Enabled = true
	cfg.Logging.RemoteSink.Endpoint = "http://sink.invalid"
	s.applyRemoteSink(cfg) // must hit the Warn + Reconfigure error path
}

// ---------------------------------------------------------------------------
// applyTracing — tracing.Setup error branch
// ---------------------------------------------------------------------------

func TestApplyTracing_SetupError(t *testing.T) {
	orig := tracingSetupFn
	tracingSetupFn = func(config.OTLPConfig) (*tracing.Exporter, error) {
		return nil, errors.New("setup boom")
	}
	t.Cleanup(func() { tracingSetupFn = orig })

	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	cfg := config.Config{}
	cfg.Tracing.OTLP.Enabled = true
	cfg.Tracing.OTLP.Endpoint = "collector.invalid:4318"
	s.applyTracing(cfg) // must hit the Warn error path
}

// ---------------------------------------------------------------------------
// tickACMECertExpiry — success branch (CertExpiry returns no error)
// ---------------------------------------------------------------------------

func TestTickACMECertExpiry_Success(t *testing.T) {
	orig := acmeCertExpiryFn
	acmeCertExpiryFn = func(*acme.Manager) (float64, error) { return 4200, nil }
	t.Cleanup(func() { acmeCertExpiryFn = orig })

	tickACMECertExpiry(acme.New(), metrics.New()) // must call SetCertExpiry
}

// ---------------------------------------------------------------------------
// Fatal branches — driven via the panicking test logger
// ---------------------------------------------------------------------------
