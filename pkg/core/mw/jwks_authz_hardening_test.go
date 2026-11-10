//go:build !no_authn

package mw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

func authzCfg(endpoint, transport string) config.Config {
	var cfg config.Config
	cfg.ExtAuthz = config.ExtAuthzConfig{
		Enabled: true, Endpoint: endpoint, Transport: transport,
		Timeout: config.DurationOf(2 * time.Second),
	}
	return cfg
}

func serveAuthz(t *testing.T, cfg config.Config) int {
	t.Helper()
	h := ExtAuthz(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logging.New(logging.Config{Out: io.Discard}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code
}

// A redirect from the decision endpoint is not followed and is not an allow.
func TestExtAuthz_RedirectNotFollowed(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { followed.Store(true) }))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	if code := serveAuthz(t, authzCfg(redirector.URL, "http")); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a redirecting authz endpoint", code)
	}
	if followed.Load() {
		t.Fatal("authz client followed the redirect")
	}
}

// An oversized OPA decision body is not read in full and does not allow.
func TestExtAuthz_OPABodyCapped(t *testing.T) {
	opa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"pad":"`+strings.Repeat("x", 2*maxAuthzResponseBytes)+`","result":true}`)
	}))
	t.Cleanup(opa.Close)
	if code := serveAuthz(t, authzCfg(opa.URL, "opa")); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an oversized OPA body", code)
	}
}

func TestAuthzClient_UnixSocketDoesNotFollowRedirects(t *testing.T) {
	if authzClient(config.ExtAuthzConfig{Endpoint: "unix:///tmp/opa.sock"}).CheckRedirect == nil {
		t.Fatal("unix-socket authz client follows redirects")
	}
}
