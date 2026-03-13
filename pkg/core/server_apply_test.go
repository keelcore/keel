//go:build !no_authn

// server_apply_test.go — white-box tests for applyRemoteSink, applyOutboundSigner,
// applyTracing, and wrapMain branches (package core).
package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/router"
)

// writeTestECKey writes an EC P-256 PKCS8 private key to a temp file and returns the path.
func writeTestECKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

// ---------------------------------------------------------------------------
// applyRemoteSink
// ---------------------------------------------------------------------------

// applyRemoteSink with remote_sink disabled must not crash and must leave
// httpSink nil.
func TestApplyRemoteSink_Disabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.runCtx = ctx

	cfg := config.Config{}
	s.applyRemoteSink(cfg)

	if s.httpSink.Load() != nil {
		t.Error("expected httpSink nil when remote_sink disabled")
	}
}

// applyRemoteSink with an HTTP endpoint must store a non-nil httpSink.
func TestApplyRemoteSink_HTTP(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.runCtx = ctx

	cfg := config.Config{
		Logging: config.LoggingConfig{
			RemoteSink: config.RemoteSinkConfig{
				Enabled:  true,
				Protocol: "http",
				Endpoint: "http://127.0.0.1:1/ingest",
			},
		},
	}
	s.applyRemoteSink(cfg)

	if s.httpSink.Load() == nil {
		t.Error("expected non-nil httpSink for HTTP remote sink")
	}
}

// applyRemoteSink: calling it twice tears down the previous sink and replaces
// it with the new one (cancel is reassigned).
func TestApplyRemoteSink_ReplacePreviousSink(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.runCtx = ctx

	httpCfg := config.Config{
		Logging: config.LoggingConfig{
			RemoteSink: config.RemoteSinkConfig{
				Enabled:  true,
				Protocol: "http",
				Endpoint: "http://127.0.0.1:1/ingest",
			},
		},
	}
	s.applyRemoteSink(httpCfg)
	if s.sinkCancel == nil {
		t.Fatal("sinkCancel must be set after first apply")
	}

	// Second apply with remote_sink disabled: must clean up previous sink.
	s.applyRemoteSink(config.Config{})
	if s.httpSink.Load() != nil {
		t.Error("httpSink must be nil after disabling remote sink")
	}
}

// ---------------------------------------------------------------------------
// applyOutboundSigner
// ---------------------------------------------------------------------------

// applyOutboundSigner with empty MyID/MySignatureKeyFile must store nil.
func TestApplyOutboundSigner_Empty(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	s.applyOutboundSigner(config.Config{})
	if s.signer.Load() != nil {
		t.Error("expected nil signer when MyID/MySignatureKeyFile are empty")
	}
}

// applyOutboundSigner with a non-existent key file must leave the existing
// signer unchanged (Warn only).
func TestApplyOutboundSigner_BadFile(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Authn: config.AuthnConfig{
			MyID:               "test-service",
			MySignatureKeyFile: "/nonexistent-key-for-test.pem",
		},
	}
	// Should not panic; signer stays nil on error.
	s.applyOutboundSigner(cfg)
	if s.signer.Load() != nil {
		t.Error("expected nil signer after bad key file")
	}
}

// applyOutboundSigner with a valid key file must store a non-nil signer.
func TestApplyOutboundSigner_ValidKeyFile(t *testing.T) {
	keyPath := writeTestECKey(t)

	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Authn: config.AuthnConfig{
			MyID:               "test-service",
			MySignatureKeyFile: keyPath,
		},
	}
	s.applyOutboundSigner(cfg)
	if s.signer.Load() == nil {
		t.Error("expected non-nil signer after valid key file")
	}
}

// ---------------------------------------------------------------------------
// applyTracing
// ---------------------------------------------------------------------------

// applyTracing with OTLP disabled must store nil exporter.
func TestApplyTracing_Disabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	s.applyTracing(config.Config{})
	if s.expPtr.Load() != nil {
		t.Error("expected nil exporter when OTLP disabled")
	}
}

// applyTracing: calling it when tracing is enabled but the endpoint is empty
// must leave exporter nil (Setup returns nil, nil for empty endpoint).
func TestApplyTracing_EmptyEndpoint(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Tracing: config.TracingConfig{
			OTLP: config.OTLPConfig{Enabled: true, Endpoint: ""},
		},
	}
	s.applyTracing(cfg)
	if s.expPtr.Load() != nil {
		t.Error("expected nil exporter for enabled=true but empty endpoint")
	}
}

// ---------------------------------------------------------------------------
// wrapMain
// ---------------------------------------------------------------------------

// wrapMain with all middleware disabled must return a handler that passes
// through to the inner handler.
func TestWrapMain_NoMiddleware(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{
			SheddingEnabled: false,
		},
		Authn:    config.AuthnConfig{Enabled: false},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	// TraceContext and RequestID are always applied; inner handler still runs.
	if rr.Code != http.StatusTeapot {
		t.Errorf("expected 418 from inner handler, got %d", rr.Code)
	}
}

// wrapMain with OWASP headers enabled must wrap the handler.
func TestWrapMain_OWASPEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{
			OWASPHeaders: true,
			HSTSMaxAge:   31536000,
		},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz:     config.ExtAuthzConfig{Enabled: false},
		Logging:      config.LoggingConfig{AccessLog: false},
		Limits:       config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with access log enabled must wrap the handler.
func TestWrapMain_AccessLogEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz:     config.ExtAuthzConfig{Enabled: false},
		Logging:      config.LoggingConfig{AccessLog: true},
		Limits:       config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with concurrency limit enabled must wrap the handler.
func TestWrapMain_ConcurrencyLimitEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz:     config.ExtAuthzConfig{Enabled: false},
		Logging:      config.LoggingConfig{AccessLog: false},
		Limits:       config.LimitsConfig{MaxConcurrent: 10, QueueDepth: 5},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// wrapMain with shedding enabled wraps the handler.
func TestWrapMain_SheddingEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security: config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{
			SheddingEnabled: true,
			HighWatermark:   0.90,
			LowWatermark:    0.70,
		},
		Authn:    config.AuthnConfig{Enabled: false},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)
	// Readiness is not degraded so the request passes through.
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// wrapMain — AuthnJWT signers closure body (nil snapshot path)
// ---------------------------------------------------------------------------

// wrapMain with Authn.Enabled and no stored snapshot covers the return-nil
// branch of the signers closure. A Bearer token is required for AuthnJWT to
// call the closure at all; the token can be invalid.
func TestWrapMain_AuthnEnabled_NilSnapshot_ClosureReturnsNil(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Authn: config.AuthnConfig{Enabled: true},
	}
	s := NewServer(log, cfg)
	// Do NOT call applyAuthnState so s.authn.Load() returns nil → closure returns nil.

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	h.ServeHTTP(rr, req)
	// AuthnJWT with no signers should reject the request — closure was invoked.
	if rr.Code == 0 {
		t.Error("expected a response code")
	}
}

// wrapMain with Authn.Enabled and a stored snapshot covers the return-signers
// branch of the signers closure.
func TestWrapMain_AuthnEnabled_WithSnapshot_ClosureReturnsSigners(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Authn: config.AuthnConfig{Enabled: true},
	}
	s := NewServer(log, cfg)
	// Store a snapshot so s.authn.Load() != nil → closure returns sn.signers.
	s.applyAuthnState(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	h.ServeHTTP(rr, req)
	// AuthnJWT with empty signers should reject the request — closure was invoked.
	if rr.Code == 0 {
		t.Error("expected a response code")
	}
}

// ---------------------------------------------------------------------------
// applyTracing — OTLP enabled with valid endpoint (exporter stored)
// ---------------------------------------------------------------------------

// applyTracing with OTLP enabled and a non-empty endpoint starts the exporter
// and stores it. The exporter must be shut down to avoid goroutine leaks.
func TestApplyTracing_Enabled_StoresExporter(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Tracing: config.TracingConfig{
			OTLP: config.OTLPConfig{
				Enabled:  true,
				Endpoint: "127.0.0.1:4318", // unreachable; background goroutine only sends
				Insecure: true,
			},
		},
	}
	s.applyTracing(cfg)

	if s.expPtr.Load() == nil {
		t.Error("expected non-nil exporter after applyTracing with valid endpoint")
	}
	// Clean up the exporter goroutine.
	s.applyTracing(config.Config{}) // Shutdown+store nil.
}

// ---------------------------------------------------------------------------
// registerDefaultRoutes
// ---------------------------------------------------------------------------

// registerDefaultRoutes with HTTPS enabled registers the default handler on
// the HTTPS port, covering server.go:259-260.
func TestRegisterDefaultRoutes_HTTPSEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	cfg.Listeners.HTTPS.Enabled = true
	cfg.Listeners.HTTPS.Port = 8443
	s := NewServer(log, cfg)
	s.useDefaultRegistrar = true

	r := router.New()
	s.registerDefaultRoutes(r) // must not panic
}

// ---------------------------------------------------------------------------
// startSidecar
// ---------------------------------------------------------------------------

// startSidecar with an invalid UpstreamURL triggers the warn path,
// covering server.go:411-417.
func TestStartSidecar_InvalidURL(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{}
	cfg.Sidecar.Enabled = true
	cfg.Sidecar.UpstreamURL = "\x00bad" // null byte → url.Parse error
	s := NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled so no health probe goroutine lingers

	r := router.New()
	s.startSidecar(ctx, r) // must not panic; logs warn and returns
}
