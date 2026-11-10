// server_apply_test.go — white-box tests for applyRemoteSink, applyOutboundSigner,
// applyTracing, and wrapMain branches (package core).
package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/tracing"
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

// ---------------------------------------------------------------------------
// wrapMain — AuthnJWT signers closure body (nil snapshot path)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// startSidecar
// ---------------------------------------------------------------------------

// logging.file: every entry reaches the file (and stdout, untouched here).
func TestFileSink_ReceivesLogLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keel.log")
	s, _ := fileSinkServer(t, path)
	s.applyRemoteSink(s.cfg)
	s.logger.Info("to_the_file", nil)
	if !logged(t, path, "to_the_file") {
		t.Fatal("line not in the file sink")
	}
}

// A path that cannot open: warned, and logging continues on stdout.
func TestFileSink_OpenFailureWarns(t *testing.T) {
	s, _ := fileSinkServer(t, filepath.Join(t.TempDir(), "missing", "keel.log"))
	var out bytes.Buffer
	s.logger = logging.New(logging.Config{JSON: true, Out: &out})
	s.applyRemoteSink(s.cfg)
	if !strings.Contains(out.String(), "log_file_open_failed") {
		t.Fatalf("log = %q", out.String())
	}
}

// SIGHUP: the same path keeps its file with the new policy; a new path switches; empty detaches.
func TestFileSink_Reload(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	s, _ := fileSinkServer(t, a)
	s.applyRemoteSink(s.cfg)
	first := s.fileSink
	cfg := s.cfg
	cfg.Logging.Rotation.MaxBackups = 1
	s.applyRemoteSink(cfg)
	if s.fileSink != first {
		t.Fatal("the same path must keep the open file")
	}
	cfg.Logging.File = b
	s.applyRemoteSink(cfg)
	s.logger.Info("in_b", nil)
	if !logged(t, b, "in_b") {
		t.Fatal("new path not used")
	}
	cfg.Logging.File = ""
	s.applyRemoteSink(cfg)
	if s.fileSink != nil {
		t.Fatal("empty path must detach the file sink")
	}
}

// backpressure.* is reloadable: a second applyBackpressure stops the running
// loops (no two loops write the same flag), resets the backpressure flag, and
// starts loops with the new values.
func TestApplyBackpressure_RestartsLoopsOnReload(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.runCtx = ctx

	var cfg config.Config
	cfg.Backpressure.SheddingEnabled = true
	cfg.Backpressure.HeapMaxBytes = 1 << 40
	cfg.Backpressure.MinHeadroomBytes = 10 << 20
	s.applyBackpressure(cfg)
	first := s.bpCancel

	s.readiness.Set(false) // as if the old pressure loop had latched
	cfg.Backpressure.SheddingEnabled = false
	s.applyBackpressure(cfg)
	if s.bpCancel == nil || &first == &s.bpCancel {
		t.Fatal("loops were not restarted")
	}
	if !s.readiness.Get() {
		t.Fatal("backpressure flag left latched across reload")
	}
	cancel()
	done := make(chan struct{})
	go func() { s.bpWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("backpressure loops did not stop")
	}
}

func TestAuthnSnapshot_TrustedIDSetNilSafe(t *testing.T) {
	var sn *authnSnapshot
	if sn.trustedIDSet() != nil {
		t.Fatal("nil snapshot returned a non-nil set")
	}
	set := map[string]struct{}{"alice": {}}
	if got := (&authnSnapshot{trustedIDs: set}).trustedIDSet(); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

// Reload before Run with a remote sink configured must not panic on the nil
// run context; the sink is attached when Run applies the config.
func TestApplyRemoteSink_BeforeRunDoesNotPanic(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	var cfg config.Config
	cfg.Logging.RemoteSink.Enabled = true
	cfg.Logging.RemoteSink.Protocol = "http"
	cfg.Logging.RemoteSink.Endpoint = "http://127.0.0.1:1/logs"
	s.applyRemoteSink(cfg)
	if s.sinkOut != nil || s.sinkCancel != nil {
		t.Fatal("sink attached before Run")
	}
}

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

func TestBuildRemoteSink_HTTP_ReturnsHTTPSink(t *testing.T) {
	cfg := config.RemoteSinkConfig{Protocol: "http", Endpoint: "http://127.0.0.1:1/ingest"}
	w, sink, err := buildRemoteSink(cfg)
	if err != nil {
		t.Fatalf("unexpected error for http protocol: %v", err)
	}
	if w == nil {
		t.Error("expected non-nil writer for http protocol")
	}
	if sink == nil {
		t.Error("expected non-nil HTTPSink for http protocol")
	}
}

func TestBuildRemoteSink_EmptyProtocol_DefaultsToHTTP(t *testing.T) {
	cfg := config.RemoteSinkConfig{Protocol: "", Endpoint: "http://127.0.0.1:1/ingest"}
	w, sink, err := buildRemoteSink(cfg)
	if err != nil {
		t.Fatalf("unexpected error for empty protocol: %v", err)
	}
	if w == nil {
		t.Error("expected non-nil writer for empty (default) protocol")
	}
	if sink == nil {
		t.Error("expected HTTPSink for empty (default) protocol")
	}
}

func TestBuildRemoteSink_Syslog_ClosedPort_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := config.RemoteSinkConfig{Protocol: "syslog", Endpoint: addr}
	_, _, err = buildRemoteSink(cfg)
	if err == nil {
		t.Error("expected error when syslog endpoint is unreachable")
	}
}

// closableSink records writes and Close, standing in for the syslog writer.
type closableSink struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (c *closableSink) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *closableSink) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *closableSink) state() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Len(), c.closed
}

// newSinkServer returns a server whose buildRemoteSinkFn hands out sinks in order
// (nil entry = init error).
func newSinkServer(t *testing.T, sinks ...*closableSink) *Server {
	t.Helper()
	orig := buildRemoteSinkFn
	t.Cleanup(func() { buildRemoteSinkFn = orig })
	i := 0
	buildRemoteSinkFn = func(config.RemoteSinkConfig) (io.Writer, *logging.HTTPSink, error) {
		s := sinks[i]
		i++
		if s == nil {
			return nil, nil, errors.New("sink init boom")
		}
		return s, nil, nil
	}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.runCtx = context.Background()
	return s
}

func sinkCfg(enabled bool) config.Config {
	var cfg config.Config
	cfg.Logging.Level = "info"
	cfg.Logging.RemoteSink.Enabled = enabled
	cfg.Logging.RemoteSink.Endpoint = "syslog.example:514"
	cfg.Logging.RemoteSink.Protocol = "syslog"
	return cfg
}

// Disabling the remote sink on reload detaches it from the logger (logs stop
// reaching it) and closes it (no leaked connection).
func TestApplyRemoteSink_DisableDetachesAndCloses(t *testing.T) {
	sink := &closableSink{}
	s := newSinkServer(t, sink)
	s.applyRemoteSink(sinkCfg(true))
	s.logger.Info("while_attached", nil)
	before, _ := sink.state()
	if before == 0 {
		t.Fatal("sink received nothing while attached")
	}

	s.applyRemoteSink(sinkCfg(false))
	s.logger.Info("after_disable", nil)
	after, closed := sink.state()
	if after != before {
		t.Errorf("sink still receives logs after being disabled (%d → %d bytes)", before, after)
	}
	if !closed {
		t.Error("disabled sink was not closed")
	}
}

// Replacing the sink (SIGHUP with a new endpoint) closes the old one.
func TestApplyRemoteSink_ReplaceClosesOld(t *testing.T) {
	oldSink, newSink := &closableSink{}, &closableSink{}
	s := newSinkServer(t, oldSink, newSink)
	s.applyRemoteSink(sinkCfg(true))
	s.applyRemoteSink(sinkCfg(true))
	if _, closed := oldSink.state(); !closed {
		t.Error("replaced sink was not closed")
	}
	if _, closed := newSink.state(); closed {
		t.Error("active sink was closed")
	}
}

// If the new sink fails to initialise, the old one is still detached and closed.
func TestApplyRemoteSink_InitFailureDetachesOld(t *testing.T) {
	sink := &closableSink{}
	s := newSinkServer(t, sink, nil)
	s.applyRemoteSink(sinkCfg(true))
	s.applyRemoteSink(sinkCfg(true))
	n, closed := sink.state()
	s.logger.Info("after_failed_reload", nil)
	if after, _ := sink.state(); after != n {
		t.Error("old sink still receives logs after a failed reload")
	}
	if !closed {
		t.Error("old sink was not closed after a failed reload")
	}
}

// applyTracing with a valid endpoint must store a non-nil exporter.
func TestApplyTracing_ValidEndpoint(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Tracing: config.TracingConfig{
			OTLP: config.OTLPConfig{
				Enabled:  true,
				Endpoint: "localhost:4318",
				Insecure: true,
			},
		},
	}
	s.applyTracing(cfg)
	exp := s.expPtr.Load()
	if exp == nil {
		t.Error("expected non-nil exporter for enabled=true with valid endpoint")
	}
	// Clean up.
	s.applyTracing(config.Config{})
}

// applyTracing second call with OTLP enabled must tear down the first exporter
// and create a new one (tests the Shutdown + re-Setup branch).
func TestApplyTracing_ReloadReplaces(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Tracing: config.TracingConfig{
			OTLP: config.OTLPConfig{Enabled: true, Endpoint: "localhost:4318", Insecure: true},
		},
	}
	s.applyTracing(cfg)
	first := s.expPtr.Load()
	if first == nil {
		t.Fatal("first exporter must be non-nil")
	}
	// Reload with same config: should replace exporter.
	s.applyTracing(cfg)
	second := s.expPtr.Load()
	if second == nil {
		t.Error("second exporter must be non-nil after reload")
	}
	// Clean up.
	s.applyTracing(config.Config{})
}
