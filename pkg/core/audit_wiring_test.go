//go:build !no_audit

package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/protonotice"

	cryptotls "crypto/tls"
)

func auditServer(t *testing.T, path string, edit func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Audit.Enabled = true
	cfg.Audit.File = path
	if edit != nil {
		edit(&cfg)
	}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.runCtx = context.Background()
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAudit)
	return s
}

func auditText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAudit_OffRecordsNothing(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Defaults())
	s.runCtx = context.Background()
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	s.auditEvent(audit.Event{Event: "x"})
	if s.auditLog.Load() != nil {
		t.Fatal("no log expected")
	}
}

// A restart continues the same chain, which still verifies.
func TestAudit_RestartContinuesTheChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	first := auditServer(t, path, nil)
	first.auditEvent(audit.Event{Event: "one"})
	first.closeAudit()
	second := auditServer(t, path, nil)
	second.auditEvent(audit.Event{Event: "two"})
	second.closeAudit()
	if n, err := audit.VerifyFiles([]string{path}); err != nil || n != 2 {
		t.Fatalf("VerifyFiles = %d, %v", n, err)
	}
}

// An unreadable tail refuses to start a second chain.
func TestAudit_UnreadableTailIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Audit.Enabled, cfg.Audit.File = true, path
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.runCtx = context.Background()
	if err := s.applyAudit(s.cfg); err == nil {
		t.Fatal("want an error")
	}
}

// A reload with the same settings keeps the open log; one that disables it detaches.
func TestAudit_Reload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	first := s.auditLog.Load()
	if err := s.applyAudit(s.cfg); err != nil {
		t.Fatal(err)
	}
	if s.auditLog.Load() != first {
		t.Fatal("same settings must keep the log")
	}
	off := s.cfg
	off.Audit.Enabled = false
	if err := s.applyAudit(off); err != nil {
		t.Fatal(err)
	}
	if s.auditLog.Load() != nil {
		t.Fatal("disabled must detach")
	}
}

// The main chain audits authentication.
func TestWrapMain_AuditsAuthn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, func(c *config.Config) { c.Authn.Enabled = true })
	s.applyAuthnState(s.cfg)
	rec := httptest.NewRecorder()
	s.wrapMain(reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(auditText(t, path), `"event":"authn_failure"`) {
		t.Fatalf("status %d, audit %q", rec.Code, auditText(t, path))
	}
}

// A 403 from the application is audited through the main chain.
func TestWrapMain_AuditsForbidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, func(c *config.Config) { c.Authn.Enabled = false })
	forbid := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	s.wrapMain(forbid).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !strings.Contains(auditText(t, path), `"event":"forbidden"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

func TestAudit_ConfigReloadAndAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	s.cfgPaths = [2]string{filepath.Join(t.TempDir(), "missing.yaml"), ""}
	if err := s.Reload(); err == nil {
		t.Fatal("want a reload error")
	}
	text := auditText(t, path)
	if !strings.Contains(text, `"event":"config_reload"`) || !strings.Contains(text, `"outcome":"fail"`) {
		t.Fatalf("audit %q", text)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	s.ReloadHandler().ServeHTTP(httptest.NewRecorder(), r)
	if !strings.Contains(auditText(t, path), `"event":"admin_reload"`) {
		t.Fatalf("audit %q", auditText(t, path))
	}
}

func TestAudit_HandshakeRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s := auditServer(t, path, nil)
	rr, ok := s.runner.(realRunner)
	if !ok {
		t.Fatal("real runner expected")
	}
	rr.onReject(protonotice.TLSVersion, &cryptotls.ClientHelloInfo{SupportedVersions: []uint16{cryptotls.VersionTLS12}})
	text := auditText(t, path)
	if !strings.Contains(text, `"event":"tls_handshake_rejected"`) || !strings.Contains(text, "KEEL-PROTO-001") {
		t.Fatalf("audit %q", text)
	}
}

// With OIDC on, a non-browser request still reaches the JWT path, and a bad cookie key is fatal.
func TestWrapMain_OIDC(t *testing.T) {
	key := filepath.Join(t.TempDir(), "cookie.key")
	if err := os.WriteFile(key, []byte(strings.Repeat("k", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Authn.OIDC.Enabled, cfg.Authn.OIDC.Issuer, cfg.Authn.OIDC.ClientID = true, "http://127.0.0.1:1", "c"
	cfg.Authn.OIDC.RedirectURL, cfg.Authn.OIDC.CookieKeyFile = "http://localhost/oauth2/callback", key
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	s.applyAuthnState(s.cfg)
	rec := httptest.NewRecorder()
	s.wrapMain(reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 from the JWT path", rec.Code)
	}

	cfg.Authn.OIDC.CookieKeyFile = filepath.Join(t.TempDir(), "missing")
	log := logging.New(logging.Config{Out: io.Discard})
	exited := false
	log.ExitFn = func(int) { exited = true }
	bad := NewServer(log, cfg)
	bad.applyAuthnState(bad.cfg)
	rec = httptest.NewRecorder()
	bad.wrapMain(reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if !exited || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("exited %v, status %d", exited, rec.Code)
	}
}

func TestRedactedConfig_HidesTheOIDCClientSecret(t *testing.T) {
	cfg := config.Defaults()
	cfg.Authn.OIDC.ClientSecret = "topsecret"
	if redactedConfig(cfg).Authn.OIDC.ClientSecret == "topsecret" {
		t.Fatal("client secret not redacted")
	}
}
