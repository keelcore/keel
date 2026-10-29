package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// An unauthenticated request is rejected by AuthnJWT before the external
// authorizer is called, and the 401 still carries the OWASP headers.
func TestWrapMain_AuthnBeforeExtAuthz_OWASPOnRejections(t *testing.T) {
	var authzCalls atomic.Int32
	authz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authzCalls.Add(1)
	}))
	defer authz.Close()

	var cfg config.Config
	cfg.Security.OWASPHeaders = true
	cfg.Authn.Enabled = true
	cfg.ExtAuthz.Enabled = true
	cfg.ExtAuthz.Endpoint = authz.URL
	cfg.ExtAuthz.Timeout = config.Duration{Duration: time.Second}
	s := NewServer(logging.New(logging.Config{Out: io.Discard}), cfg)
	h := s.wrapMain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if rec.Header().Get("x-content-type-options") != "nosniff" {
		t.Error("401 is missing OWASP headers")
	}
	if n := authzCalls.Load(); n != 0 {
		t.Errorf("external authorizer called %d times for an unauthenticated request", n)
	}
}
