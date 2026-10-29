package core

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// Once draining, /readyz reports NOT ready — and a readiness Set(true) (as the
// sidecar health probe does each tick) cannot flip it back mid-drain.
func TestDrainingReadinessCheck(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{})
	s.registerDrainingReadinessCheck()
	if ok, failing := s.readiness.IsReady(); !ok {
		t.Fatalf("ready before draining expected, failing=%v", failing)
	}
	s.draining.Store(true)
	s.readiness.Set(true)
	ok, failing := s.readiness.IsReady()
	if ok {
		t.Fatal("still ready while draining")
	}
	if len(failing) != 1 || failing[0] != "draining: shutting down" {
		t.Fatalf("failing = %v, want [draining: shutting down]", failing)
	}
}

// A reload arriving while startup holds reloadMu waits for startup to finish.
func TestReload_WaitsForStartup(t *testing.T) {
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), config.Config{},
		WithConfigPaths("/nonexistent/keel-reload-wait.yaml", ""))
	s.reloadMu.Lock() // as Run holds it during startup
	done := make(chan error, 1)
	go func() { done <- s.Reload() }()
	select {
	case <-done:
		t.Fatal("Reload ran while startup held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	s.reloadMu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Reload did not proceed after startup released the lock")
	}
}

// Ignored env overrides are logged as env_ignored with key and value.
func TestLogIgnoredEnv(t *testing.T) {
	t.Setenv("KEEL_AUTHN", "flase")
	cfg, err := config.Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var out bytes.Buffer
	s := NewServer(logging.New(logging.Config{Out: &out, JSON: true}), cfg)
	s.logIgnoredEnv(cfg)
	if !strings.Contains(out.String(), `"msg":"env_ignored"`) || !strings.Contains(out.String(), "KEEL_AUTHN=flase") {
		t.Fatalf("missing env_ignored log: %s", out.String())
	}
}

// With OWASP headers off, the request body cap still applies (X10): turning the
// headers off must not silently lift resource limits.
func TestWrapMain_BodyLimitWithoutOWASPHeaders(t *testing.T) {
	var cfg config.Config
	cfg.Security.OWASPHeaders = false
	cfg.Security.MaxRequestBodyBytes = 8
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 64))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: body limit not enforced with OWASP headers off", rec.Code)
	}
	if rec.Header().Get("x-content-type-options") != "" {
		t.Error("OWASP headers set although disabled")
	}
}
