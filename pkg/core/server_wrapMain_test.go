//go:build !no_authn && !no_authz

// server_wrapMain_test.go — wrapMain branches requiring both authn and authz.
package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// wrapMain with authn enabled wraps the handler (missing bearer → 401).
func TestWrapMain_AuthnEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn: config.AuthnConfig{
			Enabled: true,
			// No trusted signers → any token fails; no bearer → 401.
		},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)
	s.applyAuthnState(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header → AuthnJWT returns 401.
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when authn enabled with no bearer token, got %d", rr.Code)
	}
}

// wrapMain with ExtAuthz enabled wraps the handler (unreachable endpoint + fail closed → 403).
func TestWrapMain_ExtAuthzEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn:        config.AuthnConfig{Enabled: false},
		ExtAuthz: config.ExtAuthzConfig{
			Enabled:   true,
			Endpoint:  "http://127.0.0.1:1/authz",
			Timeout:   config.DurationOf(50 * time.Millisecond),
			Transport: "http",
			FailOpen:  false,
		},
		Logging: config.LoggingConfig{AccessLog: false},
		Limits:  config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	// Unreachable authz endpoint + fail_open=false → 403.
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 when ExtAuthz enabled with unreachable endpoint (fail_closed), got %d", rr.Code)
	}
}
