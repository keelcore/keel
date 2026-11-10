//go:build !no_audit

package core

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

// A reload whose audit settings cannot apply keeps the stream it has, and warns.
func TestReload_AuditFailureWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	yaml := filepath.Join(dir, "keel.yaml")
	body := "keel:\n  audit:\n    enabled: true\n    file: " + filepath.Join(dir, "missing", "audit.log") + "\n"
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
