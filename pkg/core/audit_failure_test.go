//go:build !no_audit

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cryptotls "crypto/tls"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

type failingAuditWriter struct{}

func (failingAuditWriter) Write([]byte) (int, error) { return 0, errors.New("write boom") }

func remoteSinkSeam(t *testing.T, w io.Writer, sink *logging.HTTPSink, err error) {
	t.Helper()
	orig := buildRemoteSinkFn
	buildRemoteSinkFn = func(config.RemoteSinkConfig) (io.Writer, *logging.HTTPSink, error) { return w, sink, err }
	t.Cleanup(func() { buildRemoteSinkFn = orig })
}

func withRemoteAudit(c *config.Config) {
	c.Audit.RemoteSink.Enabled, c.Audit.RemoteSink.Endpoint = true, "http://sink.invalid"
}

// A path that cannot be opened is an error.
func TestAudit_UnopenableFile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Audit.Enabled, cfg.Audit.File = true, filepath.Join(t.TempDir(), "missing", "audit.log")
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.runCtx = context.Background()
	if err := s.applyAudit(s.cfg); err == nil || !strings.Contains(err.Error(), "audit: open") {
		t.Fatalf("err = %v", err)
	}
}

// startAudit stops the process when the stream cannot start.
func TestStartAudit_FailureIsFatal(t *testing.T) {
	cfg := config.Defaults()
	cfg.Audit.Enabled, cfg.Audit.File = true, filepath.Join(t.TempDir(), "missing", "audit.log")
	log := logging.New(logging.Config{Out: io.Discard})
	exited := false
	log.ExitFn = func(int) { exited = true }
	s := NewServer(log, cfg)
	s.runCtx = context.Background()
	s.startAudit()
	if !exited {
		t.Fatal("want a fatal exit")
	}
}

// A write failure reaches the operational log.
func TestAudit_WriteFailureIsLogged(t *testing.T) {
	remoteSinkSeam(t, failingAuditWriter{}, nil, nil)
	cfg := config.Defaults()
	cfg.Audit.Enabled = true
	withRemoteAudit(&cfg)
	var out bytes.Buffer
	s := NewServer(logging.New(logging.Config{JSON: true, Out: &out}), cfg)
	s.runCtx = context.Background()
	t.Cleanup(s.closeAudit)
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	s.auditEvent(audit.Event{Event: "one"})
	if !strings.Contains(out.String(), "audit_write_failed") {
		t.Fatalf("log %q", out.String())
	}
}

// A settings change continues the running chain.
func TestAudit_SettingsChangeContinuesTheChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	s.auditEvent(audit.Event{Event: "one"})
	next := s.cfg
	next.Audit.Rotation.MaxBackups = 3
	if err := s.applyAudit(next); err != nil {
		t.Fatal(err)
	}
	s.auditEvent(audit.Event{Event: "two"})
	if n, err := audit.VerifyFiles([]string{path}); err != nil || n != 2 {
		t.Fatalf("verify = %d, %v", n, err)
	}
}

// A remote-only stream starts a new chain, and a settings change continues it.
func TestAudit_RemoteOnly(t *testing.T) {
	var buf bytes.Buffer
	remoteSinkSeam(t, &buf, nil, nil)
	cfg := config.Defaults()
	cfg.Audit.Enabled = true
	withRemoteAudit(&cfg)
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.runCtx = context.Background()
	t.Cleanup(s.closeAudit)
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	s.auditEvent(audit.Event{Event: "one"})
	next := s.cfg
	next.Audit.RemoteSink.Endpoint = "http://other.invalid"
	if err := s.applyAudit(next); err != nil {
		t.Fatal(err)
	}
	s.auditEvent(audit.Event{Event: "two"})
	if n, err := audit.VerifyReader(&buf); err != nil || n != 2 {
		t.Fatalf("verify = %d, %v", n, err)
	}
}

// A remote sink that cannot be built closes the file already opened.
func TestAudit_RemoteSinkFailure(t *testing.T) {
	remoteSinkSeam(t, nil, nil, errors.New("build boom"))
	cfg := config.Defaults()
	cfg.Audit.Enabled, cfg.Audit.File = true, filepath.Join(t.TempDir(), "audit.log")
	withRemoteAudit(&cfg)
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.runCtx = context.Background()
	if err := s.applyAudit(s.cfg); err == nil || !strings.Contains(err.Error(), "remote sink") {
		t.Fatalf("err = %v", err)
	}
}

// An HTTP sink runs until the stream stops.
func TestAudit_HTTPSinkRunsUntilClosed(t *testing.T) {
	sink := logging.NewHTTPSink("http://sink.invalid", 8, time.Hour)
	remoteSinkSeam(t, sink, sink, nil)
	cfg := config.Defaults()
	cfg.Audit.Enabled = true
	withRemoteAudit(&cfg)
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.runCtx = context.Background()
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	s.closeAudit()
	s.sinkWg.Wait()
}

// A reload whose audit settings cannot apply keeps the stream it has, and warns.
func TestReload_AuditFailureWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	yaml := filepath.Join(dir, "keel.yaml")
	body := "audit:\n  enabled: true\n  file: " + filepath.Join(dir, "missing", "audit.log") + "\n"
	if err := os.WriteFile(yaml, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Audit.Enabled, cfg.Audit.File = true, path
	s := NewServer(logging.New(logging.Config{JSON: true, Out: &out}), cfg)
	s.runCtx = context.Background()
	t.Cleanup(s.closeAudit)
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	s.cfgPaths = [2]string{yaml, ""}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "audit_reload_failed") {
		t.Fatalf("log %q", out.String())
	}
	if !strings.Contains(auditText(t, path), `"event":"config_reload"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

// Refused handshakes reach the stream at a bounded rate, with the source address when known.
func TestAudit_HandshakeRejectionSampledWithSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	observe := s.auditedRejections(func(protonotice.Code, *cryptotls.ClientHelloInfo) {})
	for range rejectionLogsPerSecond * 5 {
		observe(protonotice.TLSVersion, &cryptotls.ClientHelloInfo{Conn: server})
	}
	n, err := audit.VerifyFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || n >= rejectionLogsPerSecond*5 {
		t.Fatalf("%d entries for %d rejections", n, rejectionLogsPerSecond*5)
	}
	if !strings.Contains(auditText(t, path), `"source_ip":"pipe"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

func TestAddrIP(t *testing.T) {
	if got := addrIP(&net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 443}); got != "203.0.113.7" {
		t.Errorf("TCP address: %q", got)
	}
	if got := addrIP(&net.UnixAddr{Name: "sock"}); got != "sock" {
		t.Errorf("no port: %q", got)
	}
}
