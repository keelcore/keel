//go:build !no_authz

package mw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// ExtAuthz allows the request through to next when the decision endpoint permits it.
func TestExtAuthz_Allow_CallsNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{ExtAuthz: config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "http",
	}}
	log := logging.New(logging.Config{Out: io.Discard})
	called := false
	h := ExtAuthz(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	h.ServeHTTP(rr, req)

	if !called || rr.Code != http.StatusOK {
		t.Errorf("expected allow → next called with 200, got called=%v code=%d", called, rr.Code)
	}
}

// ExtAuthz returns 403 and does not call next when the decision endpoint denies.
func TestExtAuthz_Deny_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := config.Config{ExtAuthz: config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "http",
	}}
	log := logging.New(logging.Config{Out: io.Discard})
	called := false
	h := ExtAuthz(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	h.ServeHTTP(rr, req)

	if called {
		t.Error("expected next NOT to be called on deny")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 on deny, got %d", rr.Code)
	}
}
