//go:build !no_audit

package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
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
